# Bundle Upgrade + Image Cleanup + Docs Update

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `citeck upgrade` command (CLI + API + Web UI), Docker image cleanup after upgrade, and update all documentation to match current code.

**Architecture:** `upgrade` is a thin wrapper: update `bundleRef` in namespace.yml → call existing reload. Image cleanup is a Docker API call (`ImagesPrune` + targeted removal of old bundle images). Docs update is mechanical — sync CLAUDE.md, config.md, operations.md, api.md with changes made in this session.

**Tech Stack:** Go (CLI/daemon), React/TypeScript (Web UI), Docker SDK

---

## File Structure

| File | Action | Responsibility |
|------|--------|---------------|
| `internal/cli/upgrade.go` | Create | CLI `citeck upgrade` command |
| `internal/daemon/routes.go` | Modify | `POST /api/v1/namespace/upgrade` handler |
| `internal/api/paths.go` | Modify | Add `NamespaceUpgrade` path constant |
| `internal/api/dto.go` | Modify | Add `UpgradeRequestDto` |
| `internal/client/client.go` | Modify | Add `UpgradeNamespace` client method |
| `internal/docker/client.go` | Modify | Add `PruneImages` and `ListImages` methods |
| `internal/cli/clean.go` | Modify | Add `citeck clean images` subcommand |
| `internal/daemon/routes_p2.go` | Modify | Fix `handleListBundles` to check local repo dir |
| `web/src/pages/Dashboard.tsx` | Modify | Add upgrade button in sidebar |
| `web/src/lib/api.ts` | Modify | Add `upgradeNamespace`, `fetchBundles` |
| `web/src/lib/types.ts` | Modify | Add `BundleInfoDto` (already exists, verify) |
| `CLAUDE.md` | Modify | Add Phase 18 with all session changes |
| `docs/config.md` | Modify | Add `--offline`, `--workspace`, workspace commands |
| `docs/operations.md` | Modify | Add offline deployment, upgrade, image cleanup |
| `docs/api.md` | Modify | Add upgrade endpoint |

---

### Task 1: `POST /api/v1/namespace/upgrade` endpoint

**Files:**
- Modify: `internal/api/paths.go`
- Modify: `internal/api/dto.go`
- Modify: `internal/daemon/routes.go`
- Modify: `internal/daemon/server.go`

The upgrade endpoint takes a new bundleRef, validates it can be resolved, updates namespace.yml, then triggers reload. This reuses 100% of the existing reload logic.

- [ ] **Step 1: Add path constant and DTO**

In `internal/api/paths.go`, add after `NamespaceReload`:
```go
NamespaceUpgrade = APIV1 + "/namespace/upgrade"
```

In `internal/api/dto.go`, add:
```go
type UpgradeRequestDto struct {
    BundleRef string `json:"bundleRef"`
}
```

- [ ] **Step 2: Implement handleUpgradeNamespace**

In `internal/daemon/routes.go`, add handler. The logic:
1. Parse `UpgradeRequestDto` from request body
2. Parse bundleRef string into `bundle.Ref`
3. Load current namespace config
4. Validate: try to resolve the new bundle (fail fast if not found)
5. Update `bundleRef` in config, write namespace.yml
6. Call existing reload logic (extract `doReload` from `handleReloadNamespace`)

Extract the reload body into a reusable `doReload(nsCfg)` method so both `handleReloadNamespace` and `handleUpgradeNamespace` can call it.

```go
func (d *Daemon) handleUpgradeNamespace(w http.ResponseWriter, r *http.Request) {
    var req api.UpgradeRequestDto
    if err := readJSON(r, &req); err != nil || req.BundleRef == "" {
        writeError(w, http.StatusBadRequest, "bundleRef required")
        return
    }
    ref, err := bundle.ParseRef(req.BundleRef)
    if err != nil {
        writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid bundleRef: %v", err))
        return
    }

    if !d.reloadMu.TryLock() {
        writeErrorCode(w, http.StatusConflict, api.ErrCodeReloadInProgress, "reload already in progress")
        return
    }
    defer d.reloadMu.Unlock()

    d.configMu.RLock()
    if d.runtime == nil || d.nsConfig == nil {
        d.configMu.RUnlock()
        writeErrorCode(w, http.StatusBadRequest, api.ErrCodeNotConfigured, "no namespace configured")
        return
    }
    nsID := d.nsConfig.ID
    currentRef := d.nsConfig.BundleRef
    d.configMu.RUnlock()

    if ref == currentRef {
        writeJSON(w, api.ActionResultDto{Success: true, Message: "already on " + req.BundleRef})
        return
    }

    // Update namespace.yml with new bundleRef
    nsCfgPath := config.ResolveNamespaceConfigPath(d.workspaceID, nsID)
    nsCfg, err := namespace.LoadNamespaceConfig(nsCfgPath)
    if err != nil {
        writeInternalError(w, fmt.Errorf("load config: %w", err))
        return
    }
    nsCfg.BundleRef = ref
    data, err := namespace.MarshalNamespaceConfig(nsCfg)
    if err != nil {
        writeInternalError(w, fmt.Errorf("marshal config: %w", err))
        return
    }
    if err := fsutil.AtomicWriteFile(nsCfgPath, data, 0o644); err != nil {
        writeInternalError(w, fmt.Errorf("write config: %w", err))
        return
    }

    slog.Info("Bundle upgrade requested", "from", currentRef, "to", ref)

    // Trigger reload with the updated config
    if err := d.doReload(); err != nil {
        writeInternalError(w, fmt.Errorf("reload after upgrade: %w", err))
        return
    }

    writeJSON(w, api.ActionResultDto{
        Success: true,
        Message: fmt.Sprintf("upgraded from %s to %s", currentRef, ref),
    })
}
```

Extract `doReload()` from `handleReloadNamespace` — move the body (from TryLock check to writeJSON) into `func (d *Daemon) doReload() error`, and make `handleReloadNamespace` call it.

- [ ] **Step 3: Register route**

In `internal/daemon/server.go` `registerRoutes`:
```go
mux.HandleFunc("POST "+api.NamespaceUpgrade, d.handleUpgradeNamespace)
```

- [ ] **Step 4: Run tests**

Run: `export PATH="/usr/local/go/bin:$PATH" && go build ./internal/... ./cmd/citeck/... && go test ./internal/daemon/... -count=1`

- [ ] **Step 5: Commit**

```bash
git add internal/api/ internal/daemon/
git commit -m "Add POST /namespace/upgrade endpoint — update bundleRef + reload"
```

---

### Task 2: `citeck upgrade` CLI command

**Files:**
- Create: `internal/cli/upgrade.go`
- Modify: `internal/cli/root.go`
- Modify: `internal/client/client.go`

- [ ] **Step 1: Add client method**

In `internal/client/client.go`:
```go
func (c *DaemonClient) UpgradeNamespace(bundleRef string) (*api.ActionResultDto, error) {
    var dto api.ActionResultDto
    err := c.post(api.NamespaceUpgrade, api.UpgradeRequestDto{BundleRef: bundleRef}, &dto)
    return &dto, err
}

func (c *DaemonClient) ListBundles() ([]api.BundleInfoDto, error) {
    var dto []api.BundleInfoDto
    err := c.get(api.Bundles, &dto)
    return dto, err
}
```

- [ ] **Step 2: Create upgrade command**

Create `internal/cli/upgrade.go`:
```go
package cli

import (
    "fmt"
    "strconv"
    "strings"

    "github.com/citeck/citeck-launcher/internal/output"
    "github.com/spf13/cobra"
)

func newUpgradeCmd() *cobra.Command {
    var list bool

    cmd := &cobra.Command{
        Use:   "upgrade [bundle-ref]",
        Short: "Upgrade to a different bundle version",
        Long:  "Change the bundle version and reload. Use --list to see available versions.",
        Args:  cobra.MaximumNArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            c, err := client.New(clientOpts())
            if err != nil {
                return fmt.Errorf("connect to daemon: %w", err)
            }
            defer c.Close()

            if list || len(args) == 0 {
                return showBundleVersions(c)
            }

            result, err := c.UpgradeNamespace(args[0])
            if err != nil {
                return fmt.Errorf("upgrade: %w", err)
            }
            output.PrintResult(result, func() {
                output.PrintText(result.Message)
            })
            return nil
        },
    }

    cmd.Flags().BoolVarP(&list, "list", "l", false, "List available bundle versions")
    return cmd
}

func showBundleVersions(c *client.DaemonClient) error {
    bundles, err := c.ListBundles()
    if err != nil {
        return fmt.Errorf("list bundles: %w", err)
    }
    if len(bundles) == 0 {
        output.PrintText("No bundles available")
        return nil
    }

    // Get current bundleRef for highlighting
    ns, _ := c.GetNamespace()
    currentRef := ""
    if ns != nil {
        currentRef = ns.BundleRef
    }

    output.PrintResult(bundles, func() {
        output.PrintText("Available bundles:")
        for _, b := range bundles {
            for _, v := range b.Versions {
                ref := b.Repo + ":" + v
                marker := "  "
                if ref == currentRef {
                    marker = "* "
                }
                output.PrintText("  %s%s", marker, ref)
            }
        }
    })
    return nil
}
```

Import `"github.com/citeck/citeck-launcher/internal/client"` at the top.

- [ ] **Step 3: Register command**

In `internal/cli/root.go`, add `newUpgradeCmd()` to `root.AddCommand(...)`.

- [ ] **Step 4: Build and test**

Run: `export PATH="/usr/local/go/bin:$PATH" && go build ./cmd/citeck/...`

- [ ] **Step 5: Commit**

```bash
git add internal/cli/upgrade.go internal/cli/root.go internal/client/client.go
git commit -m "Add citeck upgrade CLI command — list versions and upgrade bundle"
```

---

### Task 3: Fix `handleListBundles` — check local repo dir

**Files:**
- Modify: `internal/daemon/routes_p2.go`

The current `handleListBundles` only scans `data/bundles/{repo.ID}` (cloned git repos). After workspace import, bundles are in `data/repo/{repo.Path}`. Need to check both.

- [ ] **Step 1: Fix handleListBundles**

```go
func (d *Daemon) handleListBundles(w http.ResponseWriter, _ *http.Request) {
    d.configMu.RLock()
    wsCfg := d.workspaceConfig
    d.configMu.RUnlock()

    var result []api.BundleInfoDto
    if wsCfg != nil {
        for _, repo := range wsCfg.BundleRepos {
            // Check local workspace repo first (offline import), then cloned bundles dir
            bundlesDir := filepath.Join(config.DataDir(), "repo")
            if repo.Path != "" {
                bundlesDir = filepath.Join(bundlesDir, repo.Path)
            }
            if _, err := os.Stat(bundlesDir); err != nil {
                bundlesDir = config.ResolveBundlesDir(d.workspaceID, repo.ID)
                if repo.Path != "" {
                    bundlesDir = filepath.Join(bundlesDir, repo.Path)
                }
            }
            versions := bundle.ListBundleVersions(bundlesDir)
            result = append(result, api.BundleInfoDto{Repo: repo.ID, Versions: versions})
        }
    }
    if result == nil {
        result = []api.BundleInfoDto{}
    }
    writeJSON(w, result)
}
```

- [ ] **Step 2: Build and test**

Run: `export PATH="/usr/local/go/bin:$PATH" && go build ./internal/daemon/...`

- [ ] **Step 3: Commit**

```bash
git add internal/daemon/routes_p2.go
git commit -m "Fix handleListBundles: check local repo dir for offline workspace import"
```

---

### Task 4: Docker image cleanup

**Files:**
- Modify: `internal/docker/client.go`
- Modify: `internal/cli/clean.go`

- [ ] **Step 1: Add PruneImages to Docker client**

In `internal/docker/client.go`, add:
```go
// PruneUnusedImages removes dangling images and returns space reclaimed.
func (c *Client) PruneUnusedImages(ctx context.Context) (uint64, error) {
    report, err := c.cli.ImagesPrune(ctx, filters.NewArgs(filters.Arg("dangling", "true")))
    if err != nil {
        return 0, fmt.Errorf("prune images: %w", err)
    }
    return report.SpaceReclaimed, nil
}
```

Add `"github.com/docker/docker/api/types/filters"` to imports if not already there (it's already imported for GetContainers).

- [ ] **Step 2: Add `citeck clean images` subcommand**

In `internal/cli/clean.go`, change `newCleanCmd` to be a parent command with subcommands. Current `clean` behavior (orphan containers) stays as `citeck clean orphans`. Add `citeck clean images`.

Actually, simpler: add `--images` flag to existing `clean` command.

In `newCleanCmd`, add flag:
```go
var images bool
cmd.Flags().BoolVar(&images, "images", false, "Prune unused Docker images (dangling)")
```

In the command body, after orphan removal, add:
```go
if images {
    output.PrintText("\nPruning unused Docker images...")
    pruneCtx, pruneCancel := context.WithTimeout(context.Background(), 2*time.Minute)
    reclaimed, pruneErr := dc.PruneUnusedImages(pruneCtx)
    pruneCancel()
    if pruneErr != nil {
        output.Errf("Image prune failed: %v", pruneErr)
    } else {
        mb := float64(reclaimed) / (1024 * 1024)
        output.PrintText("Reclaimed %.1f MB from unused images", mb)
    }
}
```

- [ ] **Step 3: Build and test**

Run: `export PATH="/usr/local/go/bin:$PATH" && go build ./internal/... ./cmd/citeck/...`

- [ ] **Step 4: Commit**

```bash
git add internal/docker/client.go internal/cli/clean.go
git commit -m "Add Docker image cleanup: citeck clean --images prunes dangling images"
```

---

### Task 5: Web UI — upgrade button on Dashboard

**Files:**
- Modify: `web/src/lib/api.ts`
- Modify: `web/src/pages/Dashboard.tsx`
- Modify: `web/src/locales/*.ts` (all 8)

- [ ] **Step 1: Add API functions**

In `web/src/lib/api.ts`:
```typescript
export async function fetchBundles(): Promise<BundleInfoDto[]> {
    const resp = await fetchWithTimeout(`${API_BASE}/bundles`)
    if (!resp.ok) return []
    return resp.json()
}

export async function upgradeNamespace(bundleRef: string): Promise<ActionResultDto> {
    const res = await fetchWithTimeout(`${API_BASE}/namespace/upgrade`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', ...CSRF_HEADER },
        body: JSON.stringify({ bundleRef }),
    })
    if (!res.ok) throw new Error(await extractErrorMessage(res))
    return res.json()
}
```

- [ ] **Step 2: Add upgrade button to Dashboard sidebar**

In `web/src/pages/Dashboard.tsx`, add an upgrade button in the sidebar (near the bundleRef display). When clicked, fetch available bundles, show a select dialog (reuse `FormDialog`), call `upgradeNamespace`.

Add import: `import { fetchBundles, upgradeNamespace } from '../lib/api'`

Add handler:
```typescript
const handleUpgrade = useCallback(async () => {
    const bundles = await fetchBundles()
    const versions: string[] = []
    for (const b of bundles) {
        for (const v of b.versions) {
            versions.push(b.repo + ':' + v)
        }
    }
    if (versions.length === 0) {
        toast(t('upgrade.noVersions'), 'error')
        return
    }
    // Show select dialog
    const current = namespace?.bundleRef || ''
    const options = versions.filter(v => v !== current)
    if (options.length === 0) {
        toast(t('upgrade.alreadyLatest'), 'info')
        return
    }
    setUpgradeVersions(options)
    setUpgradeOpen(true)
}, [namespace, t])
```

Add state: `upgradeVersions`, `upgradeOpen`, `upgradeLoading`.

Add dialog JSX in the return (use a simple select + confirm pattern, or reuse FormDialog with a select field).

Add i18n keys to all 8 locales:
- `upgrade.title`: "Upgrade Bundle"
- `upgrade.select`: "Select version"
- `upgrade.confirm`: "Upgrade"
- `upgrade.success`: "Upgrade successful"
- `upgrade.noVersions`: "No bundle versions available"
- `upgrade.alreadyLatest`: "Already on latest version"
- `dashboard.upgrade`: "Upgrade"

- [ ] **Step 3: Run web tests**

Run: `cd web && pnpm test`

- [ ] **Step 4: Commit**

```bash
git add web/
git commit -m "Add upgrade button to Dashboard — select bundle version and upgrade"
```

---

### Task 6: Update documentation

**Files:**
- Modify: `CLAUDE.md`
- Modify: `docs/config.md`
- Modify: `docs/operations.md`
- Modify: `docs/api.md`

- [ ] **Step 1: Update CLAUDE.md**

Add after Phase 17 section:

```markdown
### Phase 18: Production Readiness — COMPLETE (2026-04-07)

**Offline workspace:**
- `citeck workspace import <zip>` — extract workspace zip to data/repo/
- `citeck install --workspace <zip>` — import + interactive install in one step
- `citeck workspace update` — manual git pull for workspace + bundle repos
- Local bundle resolution: if bundleRepo path exists in data/repo/, use it (no git clone)
- Server mode: resolver always offline (no auto-pull on startup/reload)
- `--offline` flag: skip git entirely, fatal if bundle not found locally

**Secrets simplification (server mode):**
- Always encrypt with default password "citeck", no master password prompt
- Daemon auto-unlocks on startup with default password
- Wizard: removed password step
- Desktop mode: unchanged (user-provided master password)

**Docker naming:**
- Server mode: `citeck_{app}_{ns}` (no workspace suffix)
- Desktop mode: `citeck_{app}_{ns}_{ws}` (Kotlin backward compat)

**Bundle upgrade:**
- `citeck upgrade [ref]` CLI command
- `citeck upgrade --list` shows available versions
- `POST /api/v1/namespace/upgrade` API endpoint
- Web UI upgrade button on Dashboard

**Image cleanup:**
- `citeck clean --images` prunes dangling Docker images

**Dependencies:**
- Go: docker SDK v28, go-git 5.17.2, sqlite 1.48
- Web: eslint 10, typescript 6, pnpm (replaced npm)
```

- [ ] **Step 2: Update docs/config.md**

Add workspace commands section, `--offline` flag, `--workspace` flag documentation.

- [ ] **Step 3: Update docs/operations.md**

Add offline deployment section, upgrade procedure, image cleanup.

- [ ] **Step 4: Update docs/api.md**

Add `POST /api/v1/namespace/upgrade` endpoint documentation.

- [ ] **Step 5: Commit**

```bash
git add CLAUDE.md docs/
git commit -m "Update all docs: Phase 18, offline workspace, upgrade, image cleanup"
```

---

### Task 7: Lint + test + build

- [ ] **Step 1: Run Go lint**

Run: `export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH" && golangci-lint run ./internal/... ./cmd/citeck/...`

Fix any issues.

- [ ] **Step 2: Run all Go tests**

Run: `export PATH="/usr/local/go/bin:$PATH" && go test ./internal/... -count=1`

- [ ] **Step 3: Run web lint + tests**

Run: `cd web && npx eslint . && pnpm test`

- [ ] **Step 4: Full build**

Run: `export PATH="/usr/local/go/bin:$PATH" && make build`

- [ ] **Step 5: Commit if lint fixes needed**

```bash
git add -A && git commit -m "Fix lint issues"
```

---

### Task 8: Update TODO.md

- [ ] **Step 1: Mark completed items**

Mark items 1, 2, 5 as done in TODO.md. Remove from P0 section.

- [ ] **Step 2: Commit**

```bash
git add TODO.md
git commit -m "TODO: mark upgrade, image cleanup, docs as done"
```
