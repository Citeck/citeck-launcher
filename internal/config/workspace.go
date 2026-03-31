package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// WorkspaceInfo holds metadata about a discovered workspace directory.
type WorkspaceInfo struct {
	ID         string   `json:"id"`
	Dir        string   `json:"dir"`
	Namespaces []string `json:"namespaces"`
}

// NamespaceInfo holds metadata about a namespace within a workspace.
type NamespaceInfo struct {
	WorkspaceID string `json:"workspaceId"`
	NamespaceID string `json:"namespaceId"`
	ConfigPath  string `json:"configPath"`
	RtfilesDir  string `json:"rtfilesDir"`
}

// ListWorkspaces scans the ws/ directory and returns discovered workspaces.
func ListWorkspaces() ([]WorkspaceInfo, error) {
	wsRoot := WorkspacesDir()
	entries, err := os.ReadDir(wsRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read workspaces dir: %w", err)
	}

	workspaces := make([]WorkspaceInfo, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		wsID := entry.Name()
		namespaces, err := listNamespacesInWorkspace(wsID)
		if err != nil {
			return nil, fmt.Errorf("scan workspace %q: %w", wsID, err)
		}
		workspaces = append(workspaces, WorkspaceInfo{
			ID:         wsID,
			Dir:        WorkspaceDir(wsID),
			Namespaces: namespaces,
		})
	}
	return workspaces, nil
}

// listNamespacesInWorkspace returns namespace IDs found in ws/{wsID}/ns/
func listNamespacesInWorkspace(wsID string) ([]string, error) {
	nsRoot := filepath.Join(WorkspaceDir(wsID), "ns")
	entries, err := os.ReadDir(nsRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read namespaces dir: %w", err)
	}

	namespaces := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		namespaces = append(namespaces, entry.Name())
	}
	return namespaces, nil
}

// ListAllNamespaces returns all namespace infos across all workspaces.
func ListAllNamespaces() ([]NamespaceInfo, error) {
	workspaces, err := ListWorkspaces()
	if err != nil {
		return nil, err
	}

	var result []NamespaceInfo
	for _, ws := range workspaces {
		for _, nsID := range ws.Namespaces {
			result = append(result, NamespaceInfo{
				WorkspaceID: ws.ID,
				NamespaceID: nsID,
				ConfigPath:  WorkspaceNamespaceConfigPath(ws.ID, nsID),
				RtfilesDir:  NamespaceRtfilesDir(ws.ID, nsID),
			})
		}
	}
	return result, nil
}

// ResolveNamespaceConfigPath resolves the namespace config path based on mode.
// In server mode: {home}/conf/namespace.yml
// In desktop mode: {home}/ws/{wsID}/ns/{nsID}/namespace.yml
func ResolveNamespaceConfigPath(wsID, nsID string) string {
	if !IsDesktopMode() {
		return NamespaceConfigPath()
	}
	return WorkspaceNamespaceConfigPath(wsID, nsID)
}

// ResolveVolumesBase resolves the runtime files / volumes base dir based on mode.
// In server mode: {home}/data/runtime/{nsID}
// In desktop mode: {home}/ws/{wsID}/ns/{nsID}/rtfiles
func ResolveVolumesBase(wsID, nsID string) string {
	if !IsDesktopMode() {
		return filepath.Join(DataDir(), "runtime", nsID)
	}
	return NamespaceRtfilesDir(wsID, nsID)
}

// ResolveBundlesDir resolves the bundles cache dir based on mode.
// In server mode: {home}/data/bundles/{repo}
// In desktop mode: {home}/ws/{wsID}/bundles/{repo}
func ResolveBundlesDir(wsID, repo string) string {
	if !IsDesktopMode() {
		return filepath.Join(DataDir(), "bundles", repo)
	}
	return filepath.Join(WorkspaceBundlesDir(wsID), repo)
}
