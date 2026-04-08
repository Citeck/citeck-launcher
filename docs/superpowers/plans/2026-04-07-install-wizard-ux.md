# Install Wizard UX Overhaul + Live Status Unification

**Goal:** Make `citeck install` friendly for non-DevOps users. Unify live status rendering across start/stop/status.

**Principle:** Entire wizard passable by pressing Enter (all defaults are sensible). After each prompt, erase options and leave one clean summary line.

---

## File Structure

| File | Action | Responsibility |
|------|--------|---------------|
| `internal/cli/install.go` | Rewrite | Wizard flow: 9 steps instead of 12 |
| `internal/cli/install_prompt.go` | Create | `promptNumber`, `promptText`, `promptYesNo` with ANSI cleanup |
| `internal/cli/livestatus.go` | Create | Shared `renderLiveTable` using `output.FormatTable` + `ColorizeStatus` |
| `internal/cli/start.go` | Modify | Replace `streamLiveStatus` body with `renderLiveTable` |
| `internal/cli/stop.go` | Modify | Replace `streamStopStatus` body with `renderLiveTable` |

---

### Task 1: Prompt helpers (`install_prompt.go`)

Create `internal/cli/install_prompt.go` with TTY-aware prompt functions that erase options after selection.

**Functions:**

```go
// promptNumber shows numbered list, returns selected option string.
// In TTY: erases list + input line after selection, prints "label: selected".
// In non-TTY: prints label + selected value.
func promptNumber(scanner *bufio.Scanner, label string, options []string, defaultIdx int) string

// promptText shows a text prompt with hint and default.
// In TTY: erases input line, prints "label: value".
func promptText(scanner *bufio.Scanner, label, hint, defaultVal string) string

// promptYesNo shows a yes/no prompt with hint.
// In TTY: erases input line, prints "label: Yes/No".
func promptYesNo(scanner *bufio.Scanner, label, hint string, defaultYes bool) bool
```

**ANSI cleanup logic (TTY only):**
1. Count lines printed (options + input line)
2. After user input: move cursor up N lines, clear each line
3. Print single summary line: `  label: selected_value`

Detection: `term.IsTerminal(int(os.Stdout.Fd()))`

---

### Task 2: Wizard flow rewrite (`install.go`)

Rewrite the wizard with 9 steps. Remove: Display name prompt (auto from hostname), PgAdmin prompt (default off), Snapshot ID prompt (use `citeck snapshot import` later), Remote CLI instructions, separate Remote Web UI question, separate Firewall question.

**Step 0: Welcome**

```
  Citeck Platform Setup

  This wizard will configure Citeck on this server.

  What will happen:
    1. Create namespace config  -> /opt/citeck/conf/namespace.yml
    2. Create daemon config     -> /opt/citeck/conf/daemon.yml
    3. Set up systemd service   (optional)
    4. Start the platform

  You can change settings later by editing the config files
  or running: citeck install

  Press Enter to continue...
```

**Step 1: Language** — `promptNumber` with 8 options, default "English"

**Step 2: Hostname** — `promptText`
- Label: "Server hostname or IP"
- Hint: "Used for TLS certificate and browser access"
- Default: `detectOutboundIP()` result
- After input: if `localhost` → print note "Web UI will be local only"

**Step 3: TLS** — `promptNumber`
- Options depend on hostname:
  - IP → ["Self-signed certificate (recommended)", "None (HTTP only)"]
  - Domain → ["Let's Encrypt (recommended)", "Self-signed certificate", "None (HTTP only)"]
- Default: first option (recommended)

**Step 4: Port** — `promptText`
- Label: "HTTPS port"
- Hint: "Port for platform Web UI and API access"
- Default: 443 if TLS, 80 if none
- Validate: 1-65535, check availability

**Step 5: Authentication** — `promptNumber`
- Options: ["Keycloak SSO (recommended)", "Basic Auth (login/password)"]
- Default: 1 (Keycloak)
- If Basic selected → sub-prompt for users (default "admin:admin")

**Step 6: Bundle version** — `promptNumber`
- Resolve available bundles from workspace config (same logic as current)
- Options: list with "(latest)" marker on first
- If no bundles available → skip step, use LATEST
- Default: 1 (latest)

**Step 7: System service** — `promptYesNo`
- Label: "Install as system service?"
- Hint: "Citeck will start automatically on boot. Config: /etc/systemd/system/citeck.service"
- Default: Yes
- If Yes and port != 80/443 → sub-prompt: "Open port {port} in firewall (ufw)?" with hint "Required for external access"

**Step 8: Remote Web UI** — automatic (no prompt)
- If hostname != localhost → listen on 0.0.0.0, generate mTLS cert + .p12
- If hostname == localhost → listen on 127.0.0.1, skip cert generation

**Step 9: Start** — `promptYesNo`
- Label: "Start Citeck now?"
- Hint: ""
- Default: Yes
- If Yes → fork daemon + streamLiveStatus (like current)

**Auto-derived values (no prompts):**
- `nsCfg.Name` = hostname (or "Citeck" if localhost)
- `nsCfg.PgAdmin.Enabled` = false
- `nsCfg.Snapshot` = "" (clean install)
- Web UI listen address = derived from hostname choice
- Web UI port = 7088 (check availability, bump if busy — no prompt unless conflict)

---

### Task 3: Live status unification (`livestatus.go`)

Create `internal/cli/livestatus.go` that extracts shared rendering logic.

**Function:**

```go
// renderAppTable formats apps into a table string using output.FormatTable + ColorizeStatus.
// Returns the formatted string and counts (running, total for start; stopped, total for stop).
func renderAppTable(apps []api.AppDto) (table string, running int, stopped int, total int)
```

Reuses `output.FormatTable` with headers `["APP", "STATUS", "IMAGE"]` (no CPU/MEM during start/stop — not available yet).

**Modify `streamLiveStatus` (start.go):**
- Replace manual `●/○` formatting with `renderAppTable`
- TTY: clear + reprint table. Non-TTY: print summary only on change.
- Exit when all RUNNING (or follow mode)

**Modify `streamStopStatus` (stop.go):**
- Replace manual formatting with `renderAppTable`
- Same TTY/non-TTY behavior
- Exit when all STOPPED

---

### Task 4: Cleanup and tests

- Remove old `promptChoice` function (replaced by `promptNumber`)
- Remove old `prompt` function (replaced by `promptText`)
- Remove `detectPublicIP` if unused (replaced by `detectOutboundIP` in server.go)
- Update `setupSystemd` and `setupFirewall` — merge firewall into systemd step
- Verify `make build && make test-unit` passes
- Verify `golangci-lint run` clean
- Run `cd web && pnpm test`

---

### Task 5: Documentation

- Update `docs/operations.md` — new install flow
- Update `CLAUDE.md` — Phase 19 summary
- Commit all changes
