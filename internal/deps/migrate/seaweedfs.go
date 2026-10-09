package migrate

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/msg"
)

// SeaweedMigrator upgrades one configured mini store on a copy of its volume.
type SeaweedMigrator struct{ ID deps.ID }

// SupportsPair uses the descriptor forward-compatibility policy.
func (m SeaweedMigrator) SupportsPair(a, b deps.Version) (bool, msg.Message) {
	d, ok := deps.Lookup(m.ID)
	if ok && deps.IsSeaweedFamily(d) && d.UpgradeSupport(a, b).Allowed {
		return true, msg.Message{}
	}
	if deps.MovesBackwards(a, b) {
		return false, msg.Message{}
	}
	return false, VendorNoPathProblem(string(m.ID), a.String(), b.String())
}

// Preflight checks versions, existing volumes and copy space without writing.
func (m SeaweedMigrator) Preflight(ctx context.Context, e Env, p Path) PreflightResult {
	res, _, ok := CopyPreflight(ctx, e, m.ID, p, m.SupportsPair)
	if ok {
		res.OK = len(res.Problems) == 0
	}
	return res
}

// Plan builds the shared journalled copy upgrade and retained-volume rollback.
func (m SeaweedMigrator) Plan(ctx context.Context, e Env, p Path, o PlanOptions) (*Plan, deps.MigrationJournal, error) {
	return BuildCopyUpgrade(e, seaweedCopySpec(m.ID), p, o, m.Preflight(ctx, e, p))
}
func seaweedCopySpec(id deps.ID) CopySpec {
	return CopySpec{ID: id, WaitReady: waitForSeaweed, Inventory: readSeaweedInventory}
}

const seaweedShell = "weed shell -master=127.0.0.1:9333 -filer=127.0.0.1:8888"

func seaweedVerifyCmd() []string {
	return []string{"sh", "-c", `printf 'fs.verify /buckets\n' | ` + seaweedShell}
}

var seaweedVerified = regexp.MustCompile(`(?m)^verified [0-9]+ files, error 0 files(?:, pruned 0 entries)?\s*$`)

func waitForSeaweed(ctx context.Context, e Env, c string, p StepProgress) error {
	return waitForReady(ctx, e, c, "SeaweedFS", p, func(ctx context.Context) bool {
		_, _, code, err := e.Exec(ctx, c, []string{"sh", "-c", `wget -q -O /dev/null http://127.0.0.1:8333/status`})
		return err == nil && code == 0
	})
}

const seaweedMetadataLimit = 64 << 20
const seaweedExportAcknowledgement = "meta data for http://127.0.0.1:8888/buckets is saved to /dev/fd/3"

const seaweedExportOK = "\nCITECK_META_STATUS=0\n"

func seaweedExportCmd() []string {
	// fd 3 carries the framed binary export; shell messages go to stderr, where the completion acknowledgement is required.
	// A final marker preserves weed's exit status through head's bounded pipe.
	return []string{"sh", "-c", `(printf 'fs.meta.save -o /dev/fd/3 /buckets\n' | ` + seaweedShell + ` 3>&1 1>&2; status=$?; printf '\nCITECK_META_STATUS=%s\n' "$status") | head -c 67108865`}
}
func readSeaweedInventory(ctx context.Context, e Env, c string) (Inventory, error) {
	out, _, code, err := e.Exec(ctx, c, seaweedVerifyCmd())
	if err != nil {
		return nil, fmt.Errorf("verify SeaweedFS chunks: %w", err)
	}
	if code != 0 || !seaweedVerified.MatchString(out) {
		return nil, fmt.Errorf("SeaweedFS chunk verification did not confirm zero errors")
	}
	out, stderr, code, err := e.Exec(ctx, c, seaweedExportCmd())
	if err != nil {
		return nil, fmt.Errorf("export SeaweedFS metadata: %w", err)
	}
	if code != 0 || len(out) > seaweedMetadataLimit || !strings.HasSuffix(out, seaweedExportOK) || !slices.Contains(strings.Split(stderr, "\n"), seaweedExportAcknowledgement) {
		return nil, fmt.Errorf("SeaweedFS metadata export failed or exceeded 64 MiB")
	}
	return parseSeaweedInventory([]byte(strings.TrimSuffix(out, seaweedExportOK)))
}

// StepIDs is the single-hop preview vocabulary; a running plan publishes its full route.
func (SeaweedMigrator) StepIDs() []string { return CopyStepIDs() }
