package docker

import (
	"context"
	"fmt"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/volume"
)

// VolumeInfo is the daemon-facing view of a Docker named volume scoped to a
// (workspace, namespace). Size is populated from the /system/df endpoint
// when available; -1 means "not reported by the engine".
type VolumeInfo struct {
	Name       string
	OrigName   string
	CreatedAt  string
	MountPoint string
	Size       int64
}

// ListVolumes returns volumes scoped to (namespace, workspace) by label,
// mirroring Kotlin DockerApi.getVolumes(nsRef). Workspace match is
// case-insensitive — Kotlin compares with String.equals(..., true).
func (c *Client) ListVolumes(ctx context.Context) ([]VolumeInfo, error) {
	resp, err := c.cli.VolumeList(ctx, volume.ListOptions{
		Filters: filters.NewArgs(
			filters.Arg("label", LabelNamespace+"="+c.namespace),
		),
	})
	if err != nil {
		return nil, fmt.Errorf("list volumes: %w", err)
	}
	sizes, sizeErr := c.VolumesSize(ctx)
	if sizeErr != nil {
		// /system/df may be slow or unsupported; fall through with -1 sizes.
		sizes = nil
	}
	out := make([]VolumeInfo, 0, len(resp.Volumes))
	for _, v := range resp.Volumes {
		if !strings.EqualFold(v.Labels[LabelWorkspace], c.workspace) {
			continue
		}
		size := int64(-1)
		if sz, ok := sizes[v.Name]; ok {
			size = sz
		}
		out = append(out, VolumeInfo{
			Name:       v.Name,
			OrigName:   v.Labels[LabelOrigName],
			CreatedAt:  v.CreatedAt,
			MountPoint: v.Mountpoint,
			Size:       size,
		})
	}
	return out, nil
}

// VolumesSize returns a map of volume-name → size-in-bytes from the engine's
// /system/df endpoint, matching Kotlin DockerApi.getVolumesSize. Volumes with
// no UsageData reported (size == -1) are still included so callers can
// distinguish "not yet measured" from "absent".
func (c *Client) VolumesSize(ctx context.Context) (map[string]int64, error) {
	du, err := c.cli.DiskUsage(ctx, types.DiskUsageOptions{
		Types: []types.DiskUsageObject{types.VolumeObject},
	})
	if err != nil {
		return nil, fmt.Errorf("disk usage: %w", err)
	}
	result := make(map[string]int64, len(du.Volumes))
	for _, v := range du.Volumes {
		if v == nil || v.UsageData == nil {
			continue
		}
		result[v.Name] = v.UsageData.Size
	}
	return result, nil
}

// RemoveVolume removes a Docker named volume by name, matching Kotlin
// DockerApi.deleteVolume. Force=false so volumes still in use surface an error
// to the caller — the daemon route refuses deletion while the namespace runs.
func (c *Client) RemoveVolume(ctx context.Context, name string) error {
	if err := c.cli.VolumeRemove(ctx, name, false); err != nil {
		return fmt.Errorf("remove volume %s: %w", name, err)
	}
	return nil
}

// VolumeName computes the Docker volume name for a plain (non-bind) volume
// source, matching Kotlin DockerConstants.getVolumeName byte-exactly:
// "citeck_volume_{orig}_{ns}_{ws}" with ns/ws lowercased.
// Required so two namespaces (or workspaces) sharing the same plain volume
// name in their YAML don't collide on a single Docker named volume.
func VolumeName(originalName, namespace, workspace string) string {
	return "citeck_volume_" + originalName +
		"_" + strings.ToLower(namespace) +
		"_" + strings.ToLower(workspace)
}

// CreateVolume ensures a Docker named volume exists for the given plain
// volume reference, scoped to (namespace, workspace) per the Kotlin contract.
// Lookup is by label (originalName + namespace + workspace) so a manually
// renamed but correctly labeled volume is still adopted; creation uses the
// canonical VolumeName.
func (c *Client) CreateVolume(ctx context.Context, originalName string) (string, error) {
	if existing, err := c.GetVolumeByOriginalName(ctx, originalName); err != nil {
		return "", err
	} else if existing != nil {
		return existing.Name, nil
	}

	name := VolumeName(originalName, c.namespace, c.workspace)
	vol, err := c.cli.VolumeCreate(ctx, volume.CreateOptions{
		Name: name,
		Labels: map[string]string{
			LabelLauncher:    "true",
			LabelWorkspace:   c.workspace,
			LabelNamespace:   c.namespace,
			LabelOrigName:    originalName,
			LabelComposeProj: c.composeProject(),
		},
	})
	if err != nil {
		return "", fmt.Errorf("create volume %s: %w", name, err)
	}
	return vol.Name, nil
}

// GetVolumeByOriginalName returns the volume matching (originalName, namespace,
// workspace) by label, or nil if none. Workspace match is case-insensitive to
// mirror Kotlin DockerApi.getVolumeByOriginalNameOrNull.
func (c *Client) GetVolumeByOriginalName(ctx context.Context, originalName string) (*volume.Volume, error) {
	resp, err := c.cli.VolumeList(ctx, volume.ListOptions{
		Filters: filters.NewArgs(
			filters.Arg("label", LabelNamespace+"="+c.namespace),
			filters.Arg("label", LabelOrigName+"="+originalName),
		),
	})
	if err != nil {
		return nil, fmt.Errorf("list volumes: %w", err)
	}
	for _, v := range resp.Volumes {
		if strings.EqualFold(v.Labels[LabelWorkspace], c.workspace) {
			return v, nil
		}
	}
	return nil, nil
}
