package appfiles

import (
	"embed"
	"fmt"
	"io/fs"
)

//go:embed all:postgres all:pgadmin all:keycloak all:proxy all:alfresco
var files embed.FS

// GetFiles returns all embedded files as a map of path -> content.
// Production code always consumes appfiles this way — the generator merges the
// returned map with rendered templates (proxy lua, keycloak realm JSON, etc.)
// and writeRuntimeFiles is the single code path that writes bind-mount targets.
func GetFiles() (map[string][]byte, error) {
	result := make(map[string][]byte)
	err := fs.WalkDir(files, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, readErr := files.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("read embedded %s: %w", path, readErr)
		}
		result[path] = data
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk appfiles: %w", err)
	}
	return result, nil
}
