package migrate

import (
	"bytes"
	"context"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/deps/migrate/migratetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func swBytes(n uint64, b []byte) []byte {
	return append(binary.AppendUvarint(binary.AppendUvarint(nil, n<<3|2), uint64(len(b))), b...)
}
func swMetadata(name string) []byte {
	entry := append(swBytes(1, []byte(name)), swBytes(9, []byte("file content"))...)
	full := append(swBytes(1, []byte("/buckets/ecos-content")), swBytes(2, entry)...)
	return append(binary.BigEndian.AppendUint32(nil, uint32(len(full))), full...)
}
func TestSeaweedInventoryDetectsLossAndRejectsPartialExport(t *testing.T) {
	before, err := parseSeaweedInventory(swMetadata("file"))
	require.NoError(t, err)
	same, err := parseSeaweedInventory(swMetadata("file"))
	require.NoError(t, err)
	problems, _ := before.Diff(same)
	assert.Empty(t, problems)
	changed, err := parseSeaweedInventory(swMetadata("other"))
	require.NoError(t, err)
	problems, _ = before.Diff(changed)
	assert.NotEmpty(t, problems)
	for _, raw := range [][]byte{{0}, swMetadata("file")[:8], {0, 0, 0, 0}} {
		_, parseErr := parseSeaweedInventory(raw)
		require.Error(t, parseErr)
	}
	// Protobuf field ordering must not cause a false difference.
	a, err := normalizeSeaweedProto(append(swBytes(2, swBytes(1, []byte("f"))), swBytes(1, []byte("/buckets"))...), "full")
	require.NoError(t, err)
	b, err := normalizeSeaweedProto(append(swBytes(1, []byte("/buckets")), swBytes(2, swBytes(1, []byte("f")))...), "full")
	require.NoError(t, err)
	assert.True(t, bytes.Equal(a, b))
	// Repeated group names retain order (the first group is meaningful).
	groupsA, _ := normalizeSeaweedProto(append(swBytes(12, []byte("primary")), swBytes(12, []byte("secondary"))...), "attributes")
	groupsB, _ := normalizeSeaweedProto(append(swBytes(12, []byte("secondary")), swBytes(12, []byte("primary"))...), "attributes")
	assert.NotEqual(t, groupsA, groupsB)

}
func TestSeaweedCopyPlanPreservesSourceAndRejectsMetadataLoss(t *testing.T) {
	id := deps.ID("seaweedfs")
	deps.SetExtraDependencies([]deps.Descriptor{deps.NewSeaweedDescriptor(id, string(id), string(id))})
	t.Cleanup(deps.ResetExtraDependencies)
	for _, lose := range []bool{false, true} {
		t.Run(map[bool]string{false: "preserved", true: "lost metadata"}[lose], func(t *testing.T) {
			f := migratetest.New()
			f.Volumes["seaweedfs2"] = map[string]string{"data": "original"}
			f.VolSize["seaweedfs2"] = 100
			from, to := "chrislusf/seaweedfs:4.48", "chrislusf/seaweedfs:4.49"
			f.States[id] = deps.DependencyState{Image: from}
			f.ExecFn = func(container, command string) (string, string, int, error) {
				switch {
				case strings.Contains(command, "wget"):
					return "", "", 0, nil
				case strings.Contains(command, "fs.verify"):
					return "verified 1 files, error 0 files, pruned 0 entries\n", "", 0, nil
				case strings.Contains(command, "fs.meta.save"):
					name := "file"
					if lose && container == DstContainer {
						name = "lost"
					}
					return string(swMetadata(name)) + seaweedExportOK, seaweedExportAcknowledgement + "\n", 0, nil
				default:
					return "", "", 1, nil
				}
			}
			plan, j, err := (SeaweedMigrator{ID: id}).Plan(context.Background(), f, Path{from, to}, PlanOptions{})
			require.NoError(t, err)
			err = Run(context.Background(), j2store(), j, plan, nil)
			if lose {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, map[string]string{"data": "original"}, f.Volumes["seaweedfs2"])
			if !lose {
				assert.Equal(t, map[string]string{"data": "original"}, f.Volumes["seaweedfs3"])
			}
		})
	}
}
