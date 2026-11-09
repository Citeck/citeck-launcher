package cli

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/fsutil"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/citeck/citeck-launcher/internal/storage"
	"github.com/spf13/cobra"
)

var recoveryID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

type fileRecoveryResult struct {
	Workspace string `json:"workspaceId"`
	Namespace string `json:"namespaceId"`
	File      string `json:"file"`
	Applied   bool   `json:"applied"`
	Backup    string `json:"backup,omitempty"`
}

func newRecoverFileCmd() *cobra.Command {
	var ws, ns string
	var desktop, apply bool
	cmd := &cobra.Command{
		Use:   "recover-file <runtime-file>",
		Short: "Reset one saved file edit offline, with a private backup",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			previous := config.IsDesktopMode()
			config.SetDesktopMode(desktop)
			defer config.SetDesktopMode(previous)
			if !recoveryID.MatchString(ns) || desktop && !recoveryID.MatchString(ws) || !desktop && ws != "" {
				return errors.New(t("recover.invalidTarget"))
			}
			key := args[0]
			if !fs.ValidPath(key) || key == "." || strings.ContainsAny(key, `\:`) {
				return errors.New(t("recover.invalidPath"))
			}
			release, err := lockOfflineRecovery()
			if err != nil {
				return fmt.Errorf("%s: %w", t("recover.stopFirst"), err)
			}
			defer release()
			store, err := openRecoveryStore(desktop)
			if err != nil {
				return err
			}
			defer store.Close()
			result, err := recoverSavedFile(store, ws, ns, key, apply)
			if err != nil {
				return err
			}
			if output.IsJSON() {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
			}
			message := t("recover.preview", "file", key)
			if result.Applied {
				message = t("recover.applied", "file", key, "backup", result.Backup)
			}
			cmd.Println(message)
			return nil
		},
	}
	cmd.Flags().StringVar(&ws, "workspace", "", "Desktop workspace ID (required with --desktop)")
	cmd.Flags().StringVar(&ns, "namespace", "", "Namespace ID (required)")
	cmd.Flags().BoolVar(&desktop, "desktop", false, "Use the existing desktop SQLite profile")
	cmd.Flags().BoolVar(&apply, "apply", false, "Apply the reset after backing up state and the original entry")
	return cmd
}

// Keep both a profile lock and a reserved socket. The socket also excludes
// older daemons which predate the profile lock. Never unlink an existing socket:
// a stale/ambiguous socket requires an explicit operator check, not a guess.
func lockOfflineRecovery() (func(), error) {
	lock, err := fsutil.AcquireProfileLock(config.HomeDir())
	if err != nil {
		return nil, fmt.Errorf("lock offline recovery profile: %w", err)
	}
	if err = os.MkdirAll(config.RunDir(), 0o700); err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("recovery run directory: %w", err)
	}
	listener, err := net.Listen("unix", config.SocketPath())
	if err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("reserve offline socket: %w", err)
	}
	return func() { _ = listener.Close(); _ = lock.Close() }, nil
}

func openRecoveryStore(desktop bool) (storage.Store, error) {
	store, err := storage.OpenOfflineStore(config.HomeDir(), desktop)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", t("recover.existingProfile"), err)
	}
	return store, nil
}

// Operate on raw JSON fields, not NsPersistedState: an offline repair must
// preserve fields written by newer launchers, pins, journals and detach intent.
func resetRecoveryState(raw, key string) (string, error) {
	var state map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &state); err != nil || state == nil {
		return "", errors.New(t("recover.invalidState"))
	}
	var status string
	if err := json.Unmarshal(state["status"], &status); err != nil || status == "" {
		return "", errors.New(t("recover.invalidState"))
	}
	for _, field := range []string{"snapshotImportPending", "dependencyMigration"} {
		if value := state[field]; len(value) > 0 && string(value) != "null" && string(value) != "false" {
			return "", errors.New(t("recover.pending"))
		}
	}
	var edits map[string]json.RawMessage
	if value, ok := state["editedFileEdits"]; ok {
		if err := json.Unmarshal(value, &edits); err != nil {
			return "", errors.New(t("recover.invalidState"))
		}
	}
	var legacy []string
	if value, ok := state["editedFiles"]; ok {
		if err := json.Unmarshal(value, &legacy); err != nil {
			return "", errors.New(t("recover.invalidState"))
		}
	}
	_, modern := edits[key]
	if !modern && !slices.Contains(legacy, key) {
		return "", errors.New(t("recover.notEdited", "file", key))
	}
	delete(edits, key)
	remaining := slices.DeleteFunc(legacy, func(v string) bool { return v == key })
	if len(edits) == 0 {
		delete(state, "editedFileEdits")
	} else {
		state["editedFileEdits"], _ = json.Marshal(edits)
	}
	if len(remaining) == 0 {
		delete(state, "editedFiles")
	} else {
		state["editedFiles"], _ = json.Marshal(remaining)
	}
	data, err := json.Marshal(state)
	if err != nil {
		return "", fmt.Errorf("encode recovery state: %w", err)
	}
	return string(data), nil
}

func recoverSavedFile(store storage.Store, ws, ns, key string, apply bool) (fileRecoveryResult, error) {
	result := fileRecoveryResult{Workspace: ws, Namespace: ns, File: key}
	_, ok, err := store.LoadNamespaceConfig(ws, ns)
	if err != nil {
		return result, fmt.Errorf("load recovery namespace: %w", err)
	}
	if !ok {
		return result, errors.New(t("recover.invalidTarget"))
	}
	// The namespace is identified by its store key, not a YAML id override.
	// Server boot always uses "default"; desktop inherits a missing id from
	// the selected SQLite row. Match those rules rather than guessing a path.
	if !config.IsDesktopMode() && ns != "default" {
		return result, errors.New(t("recover.invalidTarget"))
	}
	raw, ok, err := store.LoadNamespaceState(ws, ns)
	if err != nil {
		return result, fmt.Errorf("load recovery state: %w", err)
	}
	if !ok {
		return result, errors.New(t("recover.invalidState"))
	}
	next, err := resetRecoveryState(raw, key)
	if err != nil {
		return result, err
	}
	if !apply {
		return result, nil
	}
	backup, err := backupRecoveryEntry(ws, ns, key, raw)
	if err != nil {
		return result, err
	}
	result.Backup = backup.path
	// Persist last, only after the original entry and state are privately backed
	// up. On failure restore the entry; retain the backup as a recovery receipt.
	var status struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal([]byte(raw), &status)
	if err = store.SaveNamespaceState(ws, ns, status.Status, next); err != nil {
		restoreErr := backup.restore()
		backup.close()
		return result, fmt.Errorf("%s: %w", t("recover.saveFailed", "backup", backup.path), errors.Join(err, restoreErr))
	}
	backup.close()
	result.Applied = true
	return result, nil
}

type recoveryBackup struct {
	parent                 *os.Root
	archive                *os.Root
	leaf, quarantine, path string
	moved                  bool
	originalInfo           os.FileInfo
}

func (b *recoveryBackup) close() {
	if b.parent != nil {
		_ = b.parent.Close()
	}
	if b.archive != nil {
		_ = b.archive.Close()
	}
}
func (b *recoveryBackup) restore() error {
	if !b.moved {
		return nil
	}
	if _, err := b.parent.Lstat(b.leaf); !errors.Is(err, os.ErrNotExist) {
		return errors.New(t("recover.restoreFailed", "backup", b.path))
	}
	actual, err := b.parent.Lstat(b.quarantine)
	if err != nil || b.originalInfo == nil || !os.SameFile(b.originalInfo, actual) {
		return errors.New(t("recover.restoreFailed", "backup", b.path))
	}
	if err = b.parent.Rename(b.quarantine, b.leaf); err != nil {
		return fmt.Errorf("restore recovery entry: %w", err)
	}
	b.moved = false
	return nil
}

// pinOriginal validates and pins the source before any backup or mutation.
func (b *recoveryBackup) pinOriginal(home *os.Root, ws, ns, key string) error {
	relative, err := filepath.Rel(config.HomeDir(), config.ResolveVolumesBase(ws, ns))
	if err != nil {
		return fmt.Errorf("recovery runtime path: %w", err)
	}
	base, _, err := fsutil.RootedParent(home, filepath.ToSlash(relative)+"/.recovery-anchor", true)
	if err != nil {
		return fmt.Errorf("pin recovery runtime directory: %w", err)
	}
	defer base.Close()
	b.parent, b.leaf, err = fsutil.RootedParent(base, key, false)
	if errors.Is(err, os.ErrNotExist) {
		b.parent, err = base.OpenRoot(".")
		b.leaf = ""
	}
	if err != nil {
		return fmt.Errorf("pin recovery file parent: %w", err)
	}
	if b.leaf == "" {
		return nil
	}
	b.originalInfo, err = b.parent.Lstat(b.leaf)
	if errors.Is(err, os.ErrNotExist) {
		b.originalInfo = nil
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect recovery entry: %w", err)
	}
	if b.originalInfo.IsDir() {
		return errors.New(t("recover.directory"))
	}
	return nil
}

// Full state and regular-file bytes live OUTSIDE runtime mounts. A single-leaf
// rooted rename quarantines the original entry locally, without re-resolving a
// mutable directory name. A manifest records its location before the move.
func backupRecoveryEntry(ws, ns, key, raw string) (result *recoveryBackup, err error) {
	home, err := fsutil.OpenCanonicalRoot(config.HomeDir())
	if err != nil {
		return nil, fmt.Errorf("open recovery home: %w", err)
	}
	defer home.Close()
	b := &recoveryBackup{}
	success := false
	defer func() {
		if !success {
			if b.moved {
				err = errors.Join(err, b.restore())
			}
			b.close()
		}
	}()
	if operationErr := b.pinOriginal(home, ws, ns, key); operationErr != nil {
		return nil, operationErr
	}
	nonce := make([]byte, 16)
	if _, err = rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("recovery nonce: %w", err)
	}
	archiveParent, _, err := fsutil.RootedParent(home, ".file-recovery/anchor", true)
	if err != nil {
		return nil, fmt.Errorf("pin recovery archive parent: %w", err)
	}
	defer archiveParent.Close()
	if operationErr := secureRecoveryBackup(archiveParent, "."); operationErr != nil {
		return nil, operationErr
	}
	archiveName := fmt.Sprintf("file-%x", nonce)
	if err = archiveParent.Mkdir(archiveName, 0o700); err != nil {
		return nil, fmt.Errorf("create recovery archive: %w", err)
	}
	b.archive, err = archiveParent.OpenRoot(archiveName)
	if err != nil {
		return nil, fmt.Errorf("pin recovery archive: %w", err)
	}
	b.path = filepath.Join(archiveParent.Name(), archiveName)
	if operationErr := secureRecoveryBackup(b.archive, "."); operationErr != nil {
		return nil, operationErr
	}
	if operationErr := fsutil.AtomicWriteRooted(b.archive, "state.json", []byte(raw), 0o600); operationErr != nil {
		return nil, fmt.Errorf("back up namespace state: %w", operationErr)
	}
	manifest := map[string]string{"workspaceId": ws, "namespaceId": ns, "file": key}
	if b.originalInfo != nil {
		b.quarantine = fmt.Sprintf(".citeck-quarantine-%x", nonce)
		manifest["originalLocation"] = filepath.Join(b.parent.Name(), b.quarantine)
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode recovery manifest: %w", err)
	}
	if operationErr := fsutil.AtomicWriteRooted(b.archive, "manifest.json", data, 0o600); operationErr != nil {
		return nil, fmt.Errorf("back up recovery manifest: %w", operationErr)
	}
	if operationErr := syncRecoveryDirectory(home); operationErr != nil {
		return nil, operationErr
	}
	if operationErr := syncRecoveryDirectory(archiveParent); operationErr != nil {
		return nil, operationErr
	}
	if operationErr := syncRecoveryDirectory(b.archive); operationErr != nil {
		return nil, operationErr
	}
	if b.originalInfo != nil {
		if err = b.parent.Rename(b.leaf, b.quarantine); err != nil {
			return nil, fmt.Errorf("quarantine recovery entry: %w", err)
		}
		b.moved = true
		if operationErr := copyRecoveryOriginal(b.parent, b.quarantine, b.archive, b.originalInfo); operationErr != nil {
			return nil, operationErr
		}
		if operationErr := syncRecoveryDirectory(b.parent); operationErr != nil {
			return nil, operationErr
		}
		if operationErr := syncRecoveryDirectory(b.archive); operationErr != nil {
			return nil, operationErr
		}
	}
	success = true
	return b, nil
}

// Special entries stay as-is at the manifest's quarantine location; never open
// them. Regular bytes are copied into the central private archive too, without
// changing the original ACL/mode, so rollback restores the original metadata.
func copyRecoveryOriginal(parent *os.Root, leaf string, archive *os.Root, expected os.FileInfo) error {
	info, err := parent.Lstat(leaf)
	if err != nil || !os.SameFile(expected, info) {
		return errors.New("quarantined entry changed before backup")
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	f, err := fsutil.OpenRegularRooted(parent, leaf)
	if err != nil {
		return fmt.Errorf("open quarantined original: %w", err)
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return errors.New("quarantined entry changed before backup")
	}
	out, err := archive.OpenFile("original", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create original backup: %w", err)
	}
	defer out.Close()
	n, err := io.Copy(out, f)
	if err != nil {
		return fmt.Errorf("copy original backup: %w", err)
	}
	after, err := f.Stat()
	if err != nil || n != opened.Size() || after.Size() != opened.Size() || !after.ModTime().Equal(opened.ModTime()) {
		return errors.New("original file changed during backup")
	}
	if err = out.Sync(); err != nil {
		return fmt.Errorf("sync original backup: %w", err)
	}
	return nil
}
