package setup

import (
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/storage"
)

const defaultSecretPassword = "citeck" //nolint:gosec // G101: well-known default, not a credential

// writePendingSecrets writes accumulated secrets via daemon API (if running)
// or directly via local SecretService. Clears PendingSecrets on success.
// Returns backup info for SecretOps tracking.
func writePendingSecrets(sctx *setupContext) (*SecretOps, error) {
	if len(sctx.PendingSecrets) == 0 {
		return nil, nil
	}

	timestamp := time.Now().UTC().Format("20060102T150405")

	// Try daemon API first (already unlocked).
	c := client.TryNew(client.Options{})
	if c != nil {
		defer c.Close()
		ops, err := writePendingSecretsDaemon(sctx, c, timestamp)
		if err != nil {
			return nil, err
		}
		clear(sctx.PendingSecrets)
		return ops, nil
	}

	// Fall back to direct SecretService.
	svc, err := openLocalSecretService()
	if err != nil {
		return nil, err
	}
	ops, err := writePendingSecretsLocal(sctx, svc, timestamp)
	if err != nil {
		return nil, err
	}
	slog.Info("Secrets written via local SecretService", "count", len(sctx.PendingSecrets))
	clear(sctx.PendingSecrets)
	return ops, nil
}

// writePendingSecretsDaemon writes secrets via daemon API with backup support.
// Note: daemon API does not expose GetSecret, so backup is attempted via local SecretService.
func writePendingSecretsDaemon(sctx *setupContext, c *client.DaemonClient, timestamp string) (*SecretOps, error) {
	// Try to open local SecretService for backup reads (best-effort).
	localSvc, localErr := openLocalSecretService()
	if localErr != nil {
		slog.Debug("Cannot open local SecretService for backup reads", "err", localErr)
	}

	ops := &SecretOps{}
	keys := sortedKeys(sctx.PendingSecrets)
	for _, key := range keys {
		value := sctx.PendingSecrets[key]
		backupKey := key + "._backup." + timestamp
		hasBackup := false

		// Attempt backup via local SecretService.
		if localSvc != nil {
			if oldVal, oldErr := localSvc.GetSecret(key); oldErr == nil && oldVal != nil && oldVal.Value != "" {
				if bErr := c.SaveSecret(backupKey, oldVal.Value); bErr != nil {
					slog.Warn("Failed to backup secret via daemon", "key", key, "err", bErr)
				} else {
					hasBackup = true
				}
			}
		}

		if err := c.SaveSecret(key, value); err != nil {
			return nil, fmt.Errorf("save secret %s via daemon: %w", key, err)
		}

		fwd := SecretOp{Key: key}
		rev := SecretOp{Key: key}
		if hasBackup {
			fwd.Backup = backupKey
			rev.Restore = backupKey
		}
		ops.Forward = append(ops.Forward, fwd)
		ops.Reverse = append(ops.Reverse, rev)
	}
	return ops, nil
}

// writePendingSecretsLocal writes secrets via local SecretService with backup support.
func writePendingSecretsLocal(sctx *setupContext, svc *storage.SecretService, timestamp string) (*SecretOps, error) {
	ops := &SecretOps{}
	keys := sortedKeys(sctx.PendingSecrets)
	for _, key := range keys {
		value := sctx.PendingSecrets[key]
		backupKey := key + "._backup." + timestamp
		hasBackup := false

		// Attempt to read old value for backup.
		if oldVal, oldErr := svc.GetSecret(key); oldErr == nil && oldVal != nil && oldVal.Value != "" {
			if bErr := svc.SaveSecret(storage.Secret{
				SecretMeta: storage.SecretMeta{ID: backupKey},
				Value:      oldVal.Value,
			}); bErr != nil {
				slog.Warn("Failed to backup secret locally", "key", key, "err", bErr)
			} else {
				hasBackup = true
			}
		}

		if err := svc.SaveSecret(storage.Secret{
			SecretMeta: storage.SecretMeta{ID: key},
			Value:      value,
		}); err != nil {
			return nil, fmt.Errorf("save secret %s: %w", key, err)
		}

		fwd := SecretOp{Key: key}
		rev := SecretOp{Key: key}
		if hasBackup {
			fwd.Backup = backupKey
			rev.Restore = backupKey
		}
		ops.Forward = append(ops.Forward, fwd)
		ops.Reverse = append(ops.Reverse, rev)
	}
	return ops, nil
}

// rollbackSecrets restores secrets from backup keys or deletes them as specified.
// Uses daemon-first-then-local pattern. Best-effort: logs warnings on failure.
func rollbackSecrets(ops []SecretOp) {
	// Try daemon API first.
	c := client.TryNew(client.Options{})
	if c != nil {
		defer c.Close()
		rollbackSecretsDaemon(ops, c)
		return
	}

	// Fall back to direct SecretService.
	svc, err := openLocalSecretService()
	if err != nil {
		slog.Warn("Cannot open SecretService for secret rollback", "err", err)
		return
	}
	rollbackSecretsLocal(ops, svc)
}

// rollbackSecretsDaemon restores secrets via daemon API. Best-effort.
func rollbackSecretsDaemon(ops []SecretOp, c *client.DaemonClient) {
	// Backup reads require local SecretService (daemon API has no GetSecret).
	localSvc, localErr := openLocalSecretService()
	if localErr != nil {
		slog.Debug("Cannot open local SecretService for backup reads during rollback", "err", localErr)
	}

	for _, op := range ops {
		switch {
		case op.Restore != "" && localSvc != nil:
			restoreSecretViaDaemon(op, localSvc, c)
		case op.Delete:
			if err := c.DeleteSecret(op.Key); err != nil {
				slog.Warn("Secret rollback: failed to delete via daemon", "key", op.Key, "err", err)
			}
		}
	}
}

// restoreSecretViaDaemon reads a backup from local SecretService, writes to daemon, and deletes the backup.
func restoreSecretViaDaemon(op SecretOp, localSvc *storage.SecretService, c *client.DaemonClient) {
	backup, err := localSvc.GetSecret(op.Restore)
	if err != nil || backup == nil || backup.Value == "" {
		slog.Warn("Secret rollback: backup not found", "key", op.Key, "backup", op.Restore)
		return
	}
	if err := c.SaveSecret(op.Key, backup.Value); err != nil {
		slog.Warn("Secret rollback: failed to restore via daemon", "key", op.Key, "err", err)
		return
	}
	if err := c.DeleteSecret(op.Restore); err != nil {
		slog.Warn("Secret rollback: failed to delete backup via daemon", "backup", op.Restore, "err", err)
	}
}

// rollbackSecretsLocal restores secrets via local SecretService. Best-effort.
func rollbackSecretsLocal(ops []SecretOp, svc *storage.SecretService) {
	for _, op := range ops {
		switch {
		case op.Restore != "":
			restoreSecretLocal(op, svc)
		case op.Delete:
			if err := svc.DeleteSecret(op.Key); err != nil {
				slog.Warn("Secret rollback: failed to delete locally", "key", op.Key, "err", err)
			}
		}
	}
}

// restoreSecretLocal reads a backup from SecretService, restores the original key, and deletes the backup.
func restoreSecretLocal(op SecretOp, svc *storage.SecretService) {
	backup, err := svc.GetSecret(op.Restore)
	if err != nil || backup == nil || backup.Value == "" {
		slog.Warn("Secret rollback: backup not found", "key", op.Key, "backup", op.Restore)
		return
	}
	if err := svc.SaveSecret(storage.Secret{
		SecretMeta: storage.SecretMeta{ID: op.Key},
		Value:      backup.Value,
	}); err != nil {
		slog.Warn("Secret rollback: failed to restore locally", "key", op.Key, "err", err)
		return
	}
	if err := svc.DeleteSecret(op.Restore); err != nil {
		slog.Warn("Secret rollback: failed to delete backup locally", "backup", op.Restore, "err", err)
	}
}

// openLocalSecretService creates a FileStore + SecretService and auto-unlocks
// with the default password if applicable.
func openLocalSecretService() (*storage.SecretService, error) {
	store, err := storage.NewFileStore(config.ConfDir())
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	svc, err := storage.NewSecretService(store)
	if err != nil {
		return nil, fmt.Errorf("secret service: %w", err)
	}
	if !svc.IsEncrypted() {
		if setupErr := svc.SetMasterPassword(defaultSecretPassword, true); setupErr != nil {
			return nil, fmt.Errorf("setup encryption: %w", setupErr)
		}
	} else if svc.IsDefaultPassword() {
		if unlockErr := svc.Unlock(defaultSecretPassword); unlockErr != nil {
			return nil, fmt.Errorf("unlock: %w", unlockErr)
		}
	}
	// Custom password: svc stays locked — SaveSecret will return ErrSecretsLocked.
	return svc, nil
}

// sortedKeys returns map keys in sorted order for deterministic iteration.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
