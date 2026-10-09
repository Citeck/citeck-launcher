package storage

import (
	"crypto/sha256"
	"encoding/hex"
)

// NamespaceSecretScope identifies a namespace within its workspace.
func NamespaceSecretScope(workspaceID, namespaceID string) string {
	return "ns:" + workspaceID + "/" + namespaceID
}

// NamespaceSecretID is safe as a file name even for workspace-defined secret IDs.
func NamespaceSecretID(workspaceID, namespaceID, name string) string {
	sum := sha256.Sum256([]byte(workspaceID + "\x00" + namespaceID + "\x00" + name))
	return "ns-" + hex.EncodeToString(sum[:12])
}
