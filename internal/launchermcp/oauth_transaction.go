package launchermcp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"
)

const oauthRevocationWarning = "Revocation was NOT saved. This connection is denied in this process; retry is pending. If Launcher exits before a successful save, repeat the revocation after restart."

var errMCPUncertain = errors.New("MCP storage outcome is unconfirmed; MCP is closed until restart")

type oauthTransactionKey struct{}
type queryer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// SQL uses the outer UOW lifetime, not a Fosite callback's request context:
// modernc can interrupt a statement on cancellation, and database/sql can
// independently roll back BeginTx. Only this owner decides when to roll back.
type oauthQueries struct {
	tx  *sql.Tx
	ctx context.Context
}

func (q oauthQueries) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	// #nosec G701 -- Internal storage SQL, never request text; values stay bound.
	result, err := q.tx.ExecContext(q.ctx, query, args...)
	return result, mcpStorageError(err)
}
func (q oauthQueries) QueryRowContext(_ context.Context, query string, args ...any) *sql.Row {
	// #nosec G701 -- Internal storage SQL, never request text; values stay bound.
	return q.tx.QueryRowContext(q.ctx, query, args...)
}
func (q oauthQueries) QueryContext(_ context.Context, query string, args ...any) (*sql.Rows, error) {
	// #nosec G701 -- Internal storage SQL, never request text; values stay bound.
	rows, err := q.tx.QueryContext(q.ctx, query, args...)
	return rows, mcpStorageError(err)
}

type sqlSettings struct {
	Settings
	Epoch int64
}
type oauthTransaction struct {
	owner      *Store
	tx         *sql.Tx
	cancel     context.CancelFunc
	q          queryer
	settings   sqlSettings
	revoke     map[string]time.Time
	rolledBack bool
}

func (s *Store) transaction(ctx context.Context) *oauthTransaction {
	tx, ok := ctx.Value(oauthTransactionKey{}).(*oauthTransaction)
	if !ok || tx.owner != s {
		panic("MCP OAuth callback outside its store transaction")
	}
	return tx
}
func (s *Store) readSettings(q queryer) (v sqlSettings, err error) {
	err = q.QueryRowContext(context.Background(), "SELECT enabled,port,epoch FROM mcp_settings WHERE id=1").Scan(&v.Enabled, &v.Port, &v.Epoch)
	return
}
func (s *Store) rollback(tx *sql.Tx, cause error) error {
	if err := tx.Rollback(); err != nil {
		s.uncertain = true
		return errors.Join(errMCPUncertain, cause, err)
	}
	return cause
}
func (s *Store) commit(ctx context.Context, u *oauthTransaction) error {
	if err := ctx.Err(); err != nil {
		return s.rollback(u.tx, err)
	}
	for id, at := range s.revocationDebt {
		if _, err := u.q.ExecContext(ctx, "UPDATE oauth_grants SET revoked=1,revoked_at=coalesce(revoked_at,?) WHERE id=?", at.UnixNano(), id); err != nil {
			return s.rollback(u.tx, err)
		}
	}
	var err error
	if s.beforeCommit != nil {
		if err = s.beforeCommit(); err != nil {
			return s.rollback(u.tx, err)
		}
	}
	// The seam (or the request) may cancel after SQL completed. This is a
	// known rollback, not an ambiguous driver commit. No response is published.
	if err = ctx.Err(); err != nil {
		return s.rollback(u.tx, err)
	}
	if err = u.tx.Commit(); err != nil {
		// Commit failure is not evidence of rollback. Do not retry issuance or publish
		// guessed old settings/root. Close this MCP owner; ordinary Launcher is intact.
		cleanup := u.tx.Rollback()
		if errors.Is(cleanup, sql.ErrTxDone) {
			cleanup = nil
		}
		s.uncertain = true
		return errors.Join(errMCPUncertain, err, cleanup)
	}
	v, err := s.readSettings(s.db)
	if err != nil {
		s.uncertain = true
		return errors.Join(errMCPUncertain, err)
	}
	s.settingsCache = v.Settings
	clear(s.revocationDebt)
	s.revocationRetryAt = time.Time{}
	s.revocationRetryDelay = 0
	s.revocationWarning = ""
	return mcpStorageError(ctx.Err())
}

// The outer SQL UOW starts BEFORE NewAccessRequest's early PKCE deletion.
// Library BeginTX/Commit are no-ops; Rollback marks issuance unsuccessful.
func (s *Store) oauthWrite(ctx context.Context, fn func(context.Context) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeLocked(ctx, "", fn)
}
func (s *Store) writeRevocation(ctx context.Context, id string, fn func(context.Context) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeLocked(ctx, id, fn)
}
func (s *Store) writeLocked(ctx context.Context, revokeID string, fn func(context.Context) error) error {
	if s.closed {
		return errors.New("MCP store is closed")
	}
	if s.uncertain {
		return errMCPUncertain
	}
	if revokeID != "" {
		s.revocationDebt[revokeID] = time.Now().UTC()
	}
	defer func() {
		if len(s.revocationDebt) > 0 && s.revocationRetryAt.IsZero() {
			s.recordRevocationFailure()
		}
	}()
	c, u, err := s.begin(ctx)
	if err != nil {
		return mcpStorageError(err)
	}
	defer u.cancel()
	err = fn(c)
	maps.Copy(s.revocationDebt, u.revoke)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil || u.rolledBack {
		if err == nil {
			err = errors.New("OAuth issuance rolled back")
		}
		err = s.rollback(u.tx, err)
		// Security intent is separate from issuance rollback and survives it.
		if !s.uncertain && len(u.revoke) > 0 {
			err = errors.Join(err, s.persistDebtLocked(context.WithoutCancel(ctx)))
		}
		return mcpStorageError(err)
	}
	return s.commit(ctx, u)
}
func (s *Store) begin(ctx context.Context) (context.Context, *oauthTransaction, error) {
	if err := ctx.Err(); err != nil {
		return ctx, nil, mcpStorageError(err)
	}
	lifetime, cancel := context.WithCancel(context.WithoutCancel(ctx))
	tx, err := s.db.BeginTx(lifetime, nil)
	if err != nil {
		cancel()
		return ctx, nil, mcpStorageError(err)
	}
	u := &oauthTransaction{owner: s, tx: tx, cancel: cancel, q: oauthQueries{tx: tx, ctx: lifetime}, revoke: map[string]time.Time{}}
	u.settings, err = s.readSettings(tx)
	if err != nil {
		err = s.rollback(tx, err)
		cancel()
		return ctx, nil, err
	}
	return context.WithValue(ctx, oauthTransactionKey{}, u), u, nil
}
func (s *Store) oauthView(ctx context.Context, fn func(context.Context) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("MCP store is closed")
	}
	if s.uncertain {
		return errMCPUncertain
	}
	u := &oauthTransaction{owner: s, q: s.db}
	var err error
	u.settings, err = s.readSettings(s.db)
	if err != nil {
		return mcpStorageError(err)
	}
	return fn(context.WithValue(ctx, oauthTransactionKey{}, u))
}
func (s *Store) recordRevocationFailure() {
	s.revocationWarning = oauthRevocationWarning
	if s.revocationRetryDelay == 0 {
		s.revocationRetryDelay = time.Second
	} else {
		s.revocationRetryDelay = min(2*s.revocationRetryDelay, time.Minute)
	}
	s.revocationRetryAt = time.Now().Add(s.revocationRetryDelay)
}

// RevocationWarning reports unresolved intent without claiming crash-proof success.
func (s *Store) RevocationWarning() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.uncertain {
		return errMCPUncertain.Error()
	}
	return s.revocationWarning
}
func (s *Store) persistDebtLocked(ctx context.Context) error {
	c, u, err := s.begin(ctx)
	if err != nil {
		return mcpStorageError(err)
	}
	defer u.cancel()
	return s.commit(c, u)
}
func (s *Store) retryOAuthRevocations(now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.uncertain {
		return errMCPUncertain
	}
	if len(s.revocationDebt) == 0 || now.Before(s.revocationRetryAt) {
		return nil
	}
	err := s.persistDebtLocked(context.Background())
	if err != nil {
		s.recordRevocationFailure()
	}
	return mcpStorageError(err)
}

// Service owns one worker, alive across disable, canceled/joined before Close.
func (s *Store) runOAuthRevocationRetry(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	cleanup := time.NewTicker(5 * time.Minute)
	defer cleanup.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			_ = s.retryOAuthRevocations(now)
		case <-cleanup.C:
			_ = s.maintainOAuth(ctx)
		}
	}
}

// Bounded indexed maintenance; never discard live used-refresh replay evidence.
func (s *Store) collectOAuth(ctx context.Context) error {
	if err := s.collectRevokedOAuth(ctx); err != nil {
		return err
	}
	q := s.transaction(ctx).q
	now := time.Now().UnixNano()
	for _, stmt := range []string{
		"UPDATE oauth_pending SET status='expired' WHERE id IN (SELECT id FROM oauth_pending WHERE status IN('pending','approved') AND expiry<=? LIMIT 256)",
		"DELETE FROM oauth_pending WHERE id IN (SELECT id FROM oauth_pending WHERE expiry+300000000000<=? LIMIT 256)",
		"UPDATE oauth_grants SET revoked=1,revoked_at=coalesce(revoked_at,?) WHERE credential_issued_at IS NULL AND revoked=0 AND EXISTS(SELECT 1 FROM oauth_codes c WHERE c.grant_id=oauth_grants.id AND c.expiry<=?)",
		"DELETE FROM oauth_access WHERE signature IN (SELECT signature FROM oauth_access WHERE expiry<=? LIMIT 256)",
		"DELETE FROM oauth_grants WHERE id IN (SELECT id FROM oauth_grants WHERE deadline<=? LIMIT 256)",
		"DELETE FROM oauth_clients WHERE id IN (SELECT id FROM oauth_clients WHERE expiry<=? AND NOT EXISTS(SELECT 1 FROM oauth_grants WHERE client=oauth_clients.id) AND NOT EXISTS(SELECT 1 FROM oauth_pending WHERE client=oauth_clients.id) LIMIT 256)",
	} {
		args := []any{now}
		if strings.HasPrefix(stmt, "UPDATE oauth_grants") {
			args = append(args, now)
		}
		if _, err := q.ExecContext(ctx, stmt, args...); err != nil {
			return mcpStorageError(err)
		}
	}
	return nil
}

// mcpStorageError adds the owning boundary while retaining driver error identity.
func mcpStorageError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("MCP private storage: %w", err)
}

// Revoked credentials may be collected; live refresh replay evidence is never evicted.
func (s *Store) collectRevokedOAuth(ctx context.Context) error {
	q := s.transaction(ctx).q
	for _, table := range []string{"oauth_pkce", "oauth_codes", "oauth_access", "oauth_refresh_current", "oauth_refresh_used"} {
		if _, err := q.ExecContext(ctx, "DELETE FROM "+table+" WHERE signature IN (SELECT t.signature FROM "+table+" t JOIN oauth_grants g ON g.id=t.grant_id WHERE g.revoked=1 LIMIT 256)"); err != nil {
			return mcpStorageError(err)
		}
	}
	return nil
}

// Reads on idle ticks do not start a write transaction or commit. Collection is
// opportunistic on public admission and on the service's coarse timer.
func (s *Store) maintainOAuth(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.uncertain || len(s.revocationDebt) > 0 {
		return nil
	}
	var due bool
	now := time.Now().UnixNano()
	err := s.db.QueryRowContext(ctx, `SELECT
 EXISTS(SELECT 1 FROM oauth_pending WHERE (status IN('pending','approved') AND expiry<=?) OR expiry+300000000000<=?) OR
 EXISTS(SELECT 1 FROM oauth_access WHERE expiry<=?) OR
 EXISTS(SELECT 1 FROM oauth_grants WHERE deadline<=? OR (revoked=0 AND credential_issued_at IS NULL AND EXISTS(SELECT 1 FROM oauth_codes c WHERE c.grant_id=oauth_grants.id AND c.expiry<=?))) OR
 EXISTS(SELECT 1 FROM oauth_clients c WHERE expiry<=? AND NOT EXISTS(SELECT 1 FROM oauth_grants WHERE client=c.id) AND NOT EXISTS(SELECT 1 FROM oauth_pending WHERE client=c.id)) OR
 EXISTS(SELECT 1 FROM oauth_grants g WHERE revoked=1 AND (
 EXISTS(SELECT 1 FROM oauth_codes WHERE grant_id=g.id) OR EXISTS(SELECT 1 FROM oauth_access WHERE grant_id=g.id) OR EXISTS(SELECT 1 FROM oauth_refresh_current WHERE grant_id=g.id) OR EXISTS(SELECT 1 FROM oauth_refresh_used WHERE grant_id=g.id)))`, now, now, now, now, now, now).Scan(&due)
	if err != nil {
		return mcpStorageError(err)
	}
	if !due {
		return nil
	}
	return s.writeLocked(ctx, "", s.collectOAuth)
}
