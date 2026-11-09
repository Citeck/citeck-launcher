package launchermcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	artifactTTL           = 30 * time.Minute
	maxArtifacts          = 16
	maxArtifactBytes      = int64(64 << 30)
	maxSystemDumpBytes    = int64(256 << 20)
	maxSystemStorageBytes = int64(512 << 20)
)

type issuerKey struct{}

// TokenID identifies the authenticated issuer (static ID or namespaced OAuth
// GrantID); it is stable across refresh and is never a credential.
func TokenID(ctx context.Context) string { v, _ := ctx.Value(issuerKey{}).(string); return v }

// Artifact describes a private streaming handle, never a host filesystem path.
type Artifact struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Size        int64     `json:"size"`
	URL         string    `json:"url"`
	ExpiresAt   time.Time `json:"expiresAt"`
	Sensitivity string    `json:"sensitivity"`
}
type artifactEntry struct {
	Artifact
	tokenID   string
	file      *os.File
	cleanup   func()
	timer     *time.Timer
	temporary bool
}
type artifactStore struct {
	mu       sync.Mutex
	entries  map[string]*artifactEntry
	pending  int
	reserved int64
	closed   bool
}

// RegisterArtifact takes ownership of a safely opened regular file, including on error.
func (s *Service) RegisterArtifact(tokenID, name string, f *os.File, cleanup func(), temporary bool) (Artifact, error) {
	return s.registerArtifact(tokenID, name, f, cleanup, temporary, false)
}
func (s *Service) registerArtifact(tokenID, name string, f *os.File, cleanup func(), temporary, reserved bool) (Artifact, error) {
	success := false
	defer func() {
		if !success {
			_ = f.Close()
			if cleanup != nil {
				cleanup()
			}
		}
	}()
	if tokenID == "" {
		return Artifact{}, errors.New("artifact issuer unavailable")
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		return Artifact{}, errors.New("diagnostic artifact must be a nonempty regular file")
	}
	id := make([]byte, 32)
	if _, err = rand.Read(id); err != nil {
		return Artifact{}, fmt.Errorf("artifacts: %w", err)
	}
	a := Artifact{ID: hex.EncodeToString(id), Name: name, Size: info.Size(), ExpiresAt: time.Now().UTC().Add(artifactTTL), Sensitivity: "Contains application memory, credentials or personal data; keep private (0600)."}
	a.URL = "/artifacts/" + a.ID
	s.artifacts.mu.Lock()
	defer s.artifacts.mu.Unlock()
	if s.artifacts.closed {
		return Artifact{}, errors.New("MCP artifact service disabled")
	}
	var total, temp int64
	for _, e := range s.artifacts.entries {
		total += e.Size
		if e.temporary {
			temp += e.Size
		}
	}
	pending := s.artifacts.pending
	reservation := s.artifacts.reserved
	if reserved {
		pending--
		reservation -= maxSystemDumpBytes
	}
	if len(s.artifacts.entries)+pending >= maxArtifacts || a.Size > maxArtifactBytes-total || temporary && a.Size > maxSystemStorageBytes-temp-reservation {
		return Artifact{}, errors.New("diagnostic artifact quota exceeded")
	}
	if s.artifacts.entries == nil {
		s.artifacts.entries = map[string]*artifactEntry{}
	}
	e := &artifactEntry{Artifact: a, tokenID: tokenID, file: f, cleanup: cleanup, temporary: temporary}
	s.artifacts.entries[a.ID] = e
	e.timer = time.AfterFunc(artifactTTL, func() { s.expireArtifact(a.ID) })
	success = true
	return a, nil
}
func (s *Service) expireArtifact(id string) {
	s.artifacts.mu.Lock()
	defer s.artifacts.mu.Unlock()
	if e := s.artifacts.entries[id]; e != nil {
		e.timer.Stop()
		_ = e.file.Close()
		if e.cleanup != nil {
			e.cleanup()
		}
		delete(s.artifacts.entries, id)
	}
}
func (s *Service) closeArtifacts() {
	s.artifacts.mu.Lock()
	defer s.artifacts.mu.Unlock()
	s.artifacts.closed = true
	for id, e := range s.artifacts.entries {
		e.timer.Stop()
		_ = e.file.Close()
		if e.cleanup != nil {
			e.cleanup()
		}
		delete(s.artifacts.entries, id)
	}
}

// CreateSystemArtifact streams a quota-bounded ZIP into private temporary storage.
func (s *Service) CreateSystemArtifact(ctx context.Context, write func(io.Writer) error) (Artifact, error) {
	s.artifacts.mu.Lock()
	var temp int64
	for _, e := range s.artifacts.entries {
		if e.temporary {
			temp += e.Size
		}
	}
	if len(s.artifacts.entries)+s.artifacts.pending >= maxArtifacts || temp+s.artifacts.reserved+maxSystemDumpBytes > maxSystemStorageBytes {
		s.artifacts.mu.Unlock()
		return Artifact{}, errors.New("system artifact quota exceeded")
	}
	s.artifacts.pending++
	s.artifacts.reserved += maxSystemDumpBytes
	s.artifacts.mu.Unlock()
	reserved := true
	release := func() {
		if reserved {
			s.artifacts.mu.Lock()
			s.artifacts.pending--
			s.artifacts.reserved -= maxSystemDumpBytes
			s.artifacts.mu.Unlock()
			reserved = false
		}
	}
	defer release()
	dir := filepath.Join(filepath.Dir(s.store.path), "artifacts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Artifact{}, fmt.Errorf("artifacts: %w", err)
	}
	if err := privatePermissions(dir, true); err != nil {
		return Artifact{}, err
	}
	f, err := os.CreateTemp(dir, "system-*.zip")
	if err != nil {
		return Artifact{}, fmt.Errorf("artifacts: %w", err)
	}
	cleanup := func() { _ = os.Remove(f.Name()) }
	success := false
	defer func() {
		if !success {
			_ = f.Close()
			cleanup()
		}
	}()
	if operationErr := privatePermissions(f.Name(), false); operationErr != nil {
		return Artifact{}, fmt.Errorf("create system artifact: %w", operationErr)
	}
	if operationErr := write(&artifactWriter{ctx: ctx, w: f, left: maxSystemDumpBytes}); operationErr != nil {
		return Artifact{}, fmt.Errorf("create system artifact: %w", operationErr)
	}
	if operationErr := f.Sync(); operationErr != nil {
		return Artifact{}, fmt.Errorf("artifacts: %w", operationErr)
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return Artifact{}, fmt.Errorf("artifacts: %w", contextErr)
	}
	a, err := s.registerArtifact(TokenID(ctx), "system-dump.zip", f, cleanup, true, true)
	success = err == nil
	return a, err
}

type artifactWriter struct {
	ctx  context.Context
	w    io.Writer
	left int64
}

func (w *artifactWriter) Write(p []byte) (int, error) {
	if operationErr := w.ctx.Err(); operationErr != nil {
		return 0, fmt.Errorf("artifacts: %w", operationErr)
	}
	if int64(len(p)) > w.left {
		return 0, errors.New("system dump exceeds 256 MiB quota")
	}
	n, err := w.w.Write(p)
	w.left -= int64(n)
	if err != nil {
		return n, fmt.Errorf("write artifact bytes: %w", err)
	}
	return n, nil
}
func (s *Service) downloadArtifact(w http.ResponseWriter, r *http.Request, token authPrincipal) {
	outcome := "refused"
	defer func() {
		slog.Info("MCP artifact download", "credentialKind", strings.ReplaceAll(strings.ReplaceAll(string(token.Kind), "\r", ""), "\n", ""), "credentialId", strings.ReplaceAll(strings.ReplaceAll(token.ID, "\r", ""), "\n", ""), "operation", "diagnostic_download", "outcome", outcome)
	}()
	if r.Method != "GET" {
		http.Error(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	if token.Role != Developer {
		http.Error(w, "developer role required", http.StatusForbidden)
		return
	}
	id := r.URL.Path[len("/artifacts/"):]
	s.artifacts.mu.Lock()
	entry := s.artifacts.entries[id]
	if entry == nil || entry.tokenID != token.artifactIdentity() || !time.Now().Before(entry.ExpiresAt) {
		s.artifacts.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	info, err := entry.file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != entry.Size {
		s.artifacts.mu.Unlock()
		http.Error(w, "artifact changed or unavailable", http.StatusConflict)
		return
	}
	a := entry.Artifact
	reader := io.NewSectionReader(entry.file, 0, a.Size)
	s.artifacts.mu.Unlock()
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(30 * time.Minute))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(a.Size, 10))
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": a.Name}))
	// CopyN verifies the registered size without reading the pathname again.
	// Short/error transfers retain Content-Length; clients must discard partials.
	if _, err = io.CopyN(w, reader, a.Size); err != nil {
		outcome = "unknown"
		panic(http.ErrAbortHandler)
	}
	outcome = "completed"
}

// No download handles survive a process restart. Only our own temporary ZIPs
// are cleaned; app exports are never copied here and never removed.
func (s *Service) cleanupTemporaryArtifacts() error {
	dir := filepath.Join(filepath.Dir(s.store.path), "artifacts")
	if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if operationErr := privatePermissions(dir, true); operationErr != nil {
		return operationErr
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("artifacts: %w", err)
	}
	defer root.Close()
	f, err := root.Open(".")
	if err != nil {
		return fmt.Errorf("artifacts: %w", err)
	}
	defer f.Close()
	var cleanupErr error
	for {
		entries, e := f.ReadDir(100)
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), "system-") || !strings.HasSuffix(entry.Name(), ".zip") {
				continue
			}
			info, err := root.Lstat(entry.Name())
			if err != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("inspect temporary artifact %q: %w", entry.Name(), err))
				continue
			}
			if !info.Mode().IsRegular() {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("unexpected temporary artifact entry %q", entry.Name()))
				continue
			}
			if operationErr := root.Remove(entry.Name()); operationErr != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove temporary artifact %q: %w", entry.Name(), operationErr))
			}
		}
		if errors.Is(e, io.EOF) {
			return cleanupErr
		}
		if e != nil {
			return errors.Join(cleanupErr, fmt.Errorf("read temporary artifacts: %w", e))
		}
	}
}
