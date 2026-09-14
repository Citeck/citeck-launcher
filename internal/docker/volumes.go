package docker

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/moby/moby/api/types/volume"
	"github.com/moby/moby/client"
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
//
// Sizes are NOT computed here (Size == -1): measuring volume sizes is slow, so
// the list loads instantly and callers fetch a single volume's size lazily, on
// demand, via VolumeSize.
func (c *Client) ListVolumes(ctx context.Context) ([]VolumeInfo, error) {
	resp, err := c.cli.VolumeList(ctx, client.VolumeListOptions{
		Filters: make(client.Filters).Add("label", LabelNamespace+"="+c.namespace),
	})
	if err != nil {
		return nil, fmt.Errorf("list volumes: %w", err)
	}
	out := make([]VolumeInfo, 0, len(resp.Items))
	for _, v := range resp.Items {
		if !strings.EqualFold(v.Labels[LabelWorkspace], c.workspace) {
			continue
		}
		out = append(out, VolumeInfo{
			Name:       v.Name,
			OrigName:   v.Labels[LabelOrigName],
			CreatedAt:  v.CreatedAt,
			MountPoint: v.Mountpoint,
			Size:       -1, // computed lazily via VolumeSize
		})
	}
	return out, nil
}

// LauncherVolume is a named volume carrying the launcher's labels, listed
// host-wide rather than scoped to one (namespace, workspace). Used by
// `citeck clean` to find the volumes of namespaces the store no longer has.
type LauncherVolume struct {
	Name      string
	Namespace string
	Workspace string
	OrigName  string
}

// ListAllLauncherVolumes returns EVERY named volume with the citeck.launcher
// label, whatever namespace or workspace it belongs to — the volume counterpart
// of ListAllLauncherContainers. Sizes are not measured (that needs a container
// per volume); the caller asks for one with VolumeSize if it wants a number.
func (c *Client) ListAllLauncherVolumes(ctx context.Context) ([]LauncherVolume, error) {
	resp, err := c.cli.VolumeList(ctx, client.VolumeListOptions{
		Filters: make(client.Filters).Add("label", LabelLauncher+"=true"),
	})
	if err != nil {
		return nil, fmt.Errorf("list all launcher volumes: %w", err)
	}
	out := make([]LauncherVolume, 0, len(resp.Items))
	for _, v := range resp.Items {
		out = append(out, LauncherVolume{
			Name:      v.Name,
			Namespace: v.Labels[LabelNamespace],
			Workspace: v.Labels[LabelWorkspace],
			OrigName:  v.Labels[LabelOrigName],
		})
	}
	return out, nil
}

// EnsureUtilsImage makes sure the launcher-utils image is on the host, pulling
// it if it is not. It is the shared preamble of EVERY caller of
// RunUtilsContainer — volume sizing, snapshot export/import, the dependency
// data probe and a dependency migration's free-space check — because a utils
// container started against a missing image fails in a way that reads as the
// QUESTION failing (a volume of unknown size, a data probe that "could not
// read", a migration refused for lack of space) rather than as a missing
// image. Keeping one copy is what keeps those four answers consistent.
func (c *Client) EnsureUtilsImage(ctx context.Context) error {
	img := config.UtilsImage()
	if c.ImageExists(ctx, img) {
		return nil
	}
	slog.Info("Pulling launcher-utils image", "image", img)
	if err := c.PullImage(ctx, img, nil); err != nil {
		return fmt.Errorf("pull utils image %s: %w", img, err)
	}
	return nil
}

// VolumeSize computes the on-disk size (in bytes) of a SINGLE named volume by
// running `du` in a throwaway utils container that mounts the volume read-only.
// Unlike Docker's /system/df — which walks every volume and takes 10-25s — this
// measures only the requested volume, so the Web UI can compute sizes per row on
// demand. The utils container is the same one snapshots use to read volume data,
// so it can read the contents under rootless Docker.
func (c *Client) VolumeSize(ctx context.Context, name string) (int64, error) {
	if err := c.EnsureUtilsImage(ctx); err != nil {
		return 0, err
	}
	// busybox du: -s summarize, -k 1024-byte blocks (portable; busybox has no -b).
	out, _, err := c.RunUtilsContainer(ctx, []string{"du", "-sk", "/vol"}, []string{name + ":/vol:ro"})
	if err != nil {
		return 0, fmt.Errorf("run du for volume %s: %w", name, err)
	}
	// `du -s` prints the total as the LAST line ("<kb>\t/vol"); any permission
	// warnings precede it. Scan from the end for the first numeric first-field.
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		fields := strings.Fields(lines[i])
		if len(fields) == 0 {
			continue
		}
		if kb, perr := strconv.ParseInt(fields[0], 10, 64); perr == nil {
			return kb * 1024, nil
		}
	}
	return 0, fmt.Errorf("no numeric size in du output for volume %s: %q", name, out)
}

// RemoveVolume removes a Docker named volume by name, matching Kotlin
// DockerApi.deleteVolume. Force=false so volumes still in use surface an error
// to the caller — the daemon route refuses deletion while the namespace runs.
func (c *Client) RemoveVolume(ctx context.Context, name string) error {
	if _, err := c.cli.VolumeRemove(ctx, name, client.VolumeRemoveOptions{Force: false}); err != nil {
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
	vol, err := c.cli.VolumeCreate(ctx, client.VolumeCreateOptions{
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
	return vol.Volume.Name, nil
}

// GetVolumeByOriginalName returns the volume matching (originalName, namespace,
// workspace) by label, or nil if none. Workspace match is case-insensitive to
// mirror Kotlin DockerApi.getVolumeByOriginalNameOrNull.
func (c *Client) GetVolumeByOriginalName(ctx context.Context, originalName string) (*volume.Volume, error) {
	resp, err := c.cli.VolumeList(ctx, client.VolumeListOptions{
		Filters: make(client.Filters).
			Add("label", LabelNamespace+"="+c.namespace).
			Add("label", LabelOrigName+"="+originalName),
	})
	if err != nil {
		return nil, fmt.Errorf("list volumes: %w", err)
	}
	for _, v := range resp.Items {
		if strings.EqualFold(v.Labels[LabelWorkspace], c.workspace) {
			return &v, nil
		}
	}
	return nil, nil
}
