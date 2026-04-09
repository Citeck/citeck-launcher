# Migrate CLI User Interaction to huh TUI Library

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace all `bufio.Scanner`/`promptNumber`/`promptText`/`promptYesNo`/`fmt.Scanln` user interactions with `charmbracelet/huh` TUI forms (arrow-key Select, Input with validation, Confirm).

**Architecture:** Each file keeps its business logic intact — only the input layer changes. The shared `promptNumber`/`promptText`/`promptYesNo` helpers in `install_prompt.go` are replaced with huh-based equivalents. Password input stays on `term.ReadPassword` (huh doesn't support secure terminal password reading without echo). The `--yes` flag bypasses huh prompts where applicable.

**Tech Stack:** Go, `github.com/charmbracelet/huh` (already in go.mod), `golang.org/x/term` (password only)

---

## Interaction Points Inventory (28 total)

### install.go (14 prompts)
| Line | What | Current | Migration |
|------|------|---------|-----------|
| 112 | Language selection | `promptNumber` | `huh.Select[string]` |
| 134 | Press Enter | `scanner.Scan` | `huh.NewNote().Run()` or keep as-is (decorative, not a real prompt) |
| 148 | Hostname input | `promptText` | `huh.Input` with validation |
| 169 | TLS choice | `promptNumber` via `tlsChoice` | `huh.Select[string]` |
| 345 | TLS type menu | `promptNumber` | `huh.Select[string]` |
| 402 | LE failure recovery | `promptNumber` | `huh.Select[string]` |
| 418 | Custom cert directory | `promptText` | `huh.Input` |
| 487 | File selection | `promptNumber` | `huh.Select[string]` |
| 543 | Registry username | `promptText` | `huh.Input` |
| 547 | Registry password | `promptText` | `huh.Input` with `EchoModePassword` |
| 556 | Registry auth recovery | `promptNumber` | `huh.Select[string]` |
| 716 | Snapshot selection | `promptNumber` | `huh.Select[string]` |
| 786,822,857 | Release selection | `promptNumber` (3 levels) | `huh.Select[string]` (3 levels) |
| 994 | Firewall confirmation | `promptYesNo` | `huh.Confirm` |
| 261 | Start now? | `promptYesNo` | `huh.Confirm` |

### start.go (6 prompts)
| Line | What | Current | Migration |
|------|------|---------|-----------|
| 173 | Master password | `term.ReadPassword` | **Keep** — huh doesn't support secure password input |
| 195 | Reset confirmation | `os.Stdin.Read` | `huh.Confirm` |
| 208 | New master password | `term.ReadPassword` | **Keep** |
| 223 | Confirm password | `term.ReadPassword` | **Keep** |
| 459 | Registry username | `promptText` | `huh.Input` |
| 463 | Registry password | `promptText` | `huh.Input` with `EchoModePassword` |

### uninstall.go (1 prompt)
| Line | What | Current | Migration |
|------|------|---------|-----------|
| 94 | "drop all data" phrase | `scanner.Scan` | `huh.Input` with exact-match validation |

### clean.go (1 prompt)
| Line | What | Current | Migration |
|------|------|---------|-----------|
| 161 | Removal confirmation | `scanner.Scan` | `huh.Confirm` |

### snapshot.go (2 prompts)
| Line | What | Current | Migration |
|------|------|---------|-----------|
| 78 | Output directory | `scanner.Scan` | `huh.Input` (optional, empty = default) |
| 128 | Stop namespace? | `scanner.Scan` | `huh.Confirm` |

### workspace.go (1 prompt)
| Line | What | Current | Migration |
|------|------|---------|-----------|
| 58 | Overwrite confirmation | `fmt.Scanln` | `huh.Confirm` |

**Total: 25 prompts migrate to huh, 3 stay on `term.ReadPassword`.**

---

## File Map

### Modified files

| File | Changes |
|------|---------|
| `internal/cli/install_prompt.go` | Replace `promptNumber` → huh Select wrapper, `promptText` → huh Input wrapper, `promptYesNo` → huh Confirm wrapper. Remove `bufio.Scanner` parameter from all. Keep `printStepHeader`, `displayWidth`, `isWideRune`. |
| `internal/cli/install.go` | Remove `bufio.NewScanner(os.Stdin)`. Replace all `promptNumber`/`promptText`/`promptYesNo` calls with new wrappers (no scanner param). Replace `scanner.Scan()` welcome with `huh.NewNote` or simple Enter via huh. |
| `internal/cli/start.go` | Replace `checkRegistryAuth` prompts with huh Input. Replace `handlePasswordReset` confirmation with `huh.Confirm`. Keep `term.ReadPassword` for actual passwords. |
| `internal/cli/uninstall.go` | Replace scanner-based phrase input with `huh.Input` + exact-match validator. Remove `bufio` import. |
| `internal/cli/clean.go` | Replace scanner-based confirmation with `huh.Confirm`. |
| `internal/cli/snapshot.go` | Replace scanner prompts with `huh.Input` and `huh.Confirm`. |
| `internal/cli/workspace.go` | Replace `fmt.Scanln` with `huh.Confirm`. |

---

## Task 1: Rewrite prompt helpers in install_prompt.go

**Files:**
- Modify: `internal/cli/install_prompt.go`

Replace the three prompt functions to use huh internally. Remove `*bufio.Scanner` parameter. This is the foundation — all callers will be updated in subsequent tasks.

- [ ] **Step 1: Rewrite `promptNumber` → huh.Select wrapper**

Replace current `promptNumber(scanner, label, options, defaultIdx)` with `promptSelect(label string, options []string, defaultIdx int) string`:

```go
func promptSelect(label string, options []string, defaultIdx int) string {
    huhOpts := make([]huh.Option[string], len(options))
    for i, opt := range options {
        huhOpts[i] = huh.NewOption(opt, opt)
    }

    var selected string
    if defaultIdx >= 0 && defaultIdx < len(options) {
        selected = options[defaultIdx]
    }

    err := huh.NewSelect[string]().
        Title(label).
        Options(huhOpts...).
        Value(&selected).
        Run()
    if err != nil {
        return options[defaultIdx] // fallback to default on cancel
    }
    return selected
}
```

- [ ] **Step 2: Rewrite `promptText` → huh.Input wrapper**

Replace `promptText(scanner, label, hint, defaultVal)` with `promptInput(label, hint, defaultVal string) string`:

```go
func promptInput(label, hint, defaultVal string) string {
    var value string
    input := huh.NewInput().
        Title(label).
        Value(&value).
        Placeholder(defaultVal)
    if hint != "" {
        input = input.Description(hint)
    }

    if err := input.Run(); err != nil {
        return defaultVal
    }
    if value == "" {
        return defaultVal
    }
    return strings.TrimSpace(value)
}
```

- [ ] **Step 3: Rewrite `promptYesNo` → huh.Confirm wrapper**

Replace `promptYesNo(scanner, label, hint, defaultYes)` with `promptConfirm(label string, defaultYes bool) bool`:

```go
func promptConfirm(label string, defaultYes bool) bool {
    if flagYes {
        return defaultYes
    }
    var result bool
    result = defaultYes
    err := huh.NewConfirm().
        Title(label).
        Value(&result).
        Run()
    if err != nil {
        return defaultYes
    }
    return result
}
```

- [ ] **Step 4: Remove `clearLines` calls from prompt functions**

huh handles its own terminal clearing. Remove `clearLines` calls that were used after selection. Remove `isTTYOut` calls from prompt functions. Keep `printStepHeader` and `displayWidth` unchanged.

- [ ] **Step 5: Remove `bufio` import if no longer needed**

- [ ] **Step 6: Do NOT commit yet** — callers still use old signatures. Proceed directly to migrating install.go (next steps).

**Continue: Migrate install.go wizard (same task, same commit)**

Both `install_prompt.go` and `install.go` must be updated atomically — the code must compile at commit time.

- [ ] **Step 7: Remove scanner from install.go**

Delete `scanner := bufio.NewScanner(os.Stdin)` from `runInstall`.

- [ ] **Step 8: Replace language selection (line 112)**

```go
// Before: locale := promptNumber(scanner, "Language / Язык / 语言", langOptions, 0)
// After:
locale := promptSelect("Language / Язык / 语言", langOptions, 0)
```

- [ ] **Step 9: Replace welcome "Press Enter" (line 134)**

Replace `scanner.Scan()` with:

```go
promptConfirm(t("install.welcome.pressEnter"), true)
```

- [ ] **Step 10: Replace hostname input (line 148)**

```go
hostname = promptInput(t("install.hostname.label"), t("install.hostname.hint"), defaultHost)
```

- [ ] **Step 11: Replace all TLS prompts**

In `configureTLS` and `tryLEWithRecovery`: replace all `promptNumber(scanner,...)` with `promptSelect(...)`, all `promptText(scanner,...)` with `promptInput(...)`.

- [ ] **Step 12: Replace release selection prompts**

In `resolveRelease`, `pickRelease`, `pickOtherRelease`: replace `promptNumber(scanner,...)` with `promptSelect(...)`.

- [ ] **Step 13: Replace registry auth prompts**

In `configureRegistryAuth`: replace `promptText(scanner,...)` with `promptInput(...)`.

- [ ] **Step 14: Replace snapshot selection**

In `selectSnapshot`: replace `promptNumber(scanner,...)` with `promptSelect(...)`.

- [ ] **Step 15: Replace firewall and start confirmations**

Replace `promptYesNo(scanner,...)` with `promptConfirm(...)`.

- [ ] **Step 16: Remove all `*bufio.Scanner` parameters from helper functions**

Functions: `configureTLS`, `resolveRelease`, `pickRelease`, `pickOtherRelease`, `configureRegistryAuth`, `selectSnapshot`, `pickFileByExt`, `configureCustomCert` — remove scanner parameter.

- [ ] **Step 17: Remove `bufio` import from install.go**

- [ ] **Step 18: Build and test**

```bash
/usr/local/go/bin/go build ./...
/usr/local/go/bin/go test ./internal/cli/ -count=1
```

- [ ] **Step 19: Commit both files together**

```bash
git add internal/cli/install_prompt.go internal/cli/install.go
git commit -m "feat(wizard): migrate install wizard + prompt helpers to huh TUI"
```

---

## Task 2: Migrate start.go prompts

**Files:**
- Modify: `internal/cli/start.go`

- [ ] **Step 1: Replace handlePasswordReset confirmation (line 195)**

Replace `os.Stdin.Read` + manual parsing with `huh.Confirm`:

```go
// Before: manual buf read
// After:
var proceed bool
_ = huh.NewConfirm().Title("All secrets will be regenerated. Continue?").Value(&proceed).Run()
if !proceed {
    return "", fmt.Errorf("reset canceled")
}
```

Keep `term.ReadPassword` calls for actual password input (lines 173, 208, 223) — huh doesn't support secure password reading.

- [ ] **Step 2: Replace checkRegistryAuth prompts (lines 459, 463)**

Replace `promptText(scanner,...)` with `promptInput(...)`:

```go
username := promptInput(t("install.registry.username"), "", "")
password := promptInput(t("install.registry.password"), "", "")
```

Remove scanner creation in `checkRegistryAuth`.

- [ ] **Step 3: Build and test**

```bash
/usr/local/go/bin/go build ./...
```

- [ ] **Step 4: Commit**

```bash
git add internal/cli/start.go
git commit -m "refactor: migrate start.go prompts to huh"
```

---

## Task 3: Migrate uninstall.go, clean.go, snapshot.go, workspace.go

**Files:**
- Modify: `internal/cli/uninstall.go`
- Modify: `internal/cli/clean.go`
- Modify: `internal/cli/snapshot.go`
- Modify: `internal/cli/workspace.go`

- [ ] **Step 1: Migrate uninstall.go phrase confirmation**

Replace scanner-based input with `huh.Input` + exact-match validator:

```go
var input string
err := huh.NewInput().
    Title(t("uninstall.dataDropHint", "phrase", dropConfirmPhrase)).
    Description(t("uninstall.dataKeepHint")).
    Value(&input).
    Run()
if err != nil || !strings.EqualFold(strings.TrimSpace(input), dropConfirmPhrase) {
    output.PrintText(t("uninstall.dataPreserved", "path", homeDir))
} else {
    // delete
}
```

Remove `bufio` import.

- [ ] **Step 2: Migrate clean.go confirmation**

Replace with `huh.Confirm`:

```go
func confirmCleanRemoval(scan cleanScanResult, images bool) bool {
    if flagYes {
        return true
    }
    what := fmt.Sprintf(...)
    var proceed bool
    _ = huh.NewConfirm().Title(fmt.Sprintf("Remove %s?", what)).Value(&proceed).Run()
    return proceed
}
```

Remove `bufio` import.

- [ ] **Step 3: Migrate snapshot.go prompts**

Replace output directory prompt:
```go
var dir string
_ = huh.NewInput().Title("Output directory (empty for default)").Value(&dir).Run()
```

Replace stop confirmation:
```go
proceed := promptConfirm("Namespace is running. Stop it?", true)
```

Remove `bufio` import.

- [ ] **Step 4: Migrate workspace.go overwrite prompt**

Replace `fmt.Scanln` with `huh.Confirm`:
```go
overwrite := promptConfirm(fmt.Sprintf("Destination %s already exists. Overwrite?", destDir), false)
if !overwrite {
    return fmt.Errorf("canceled")
}
```

Remove `fmt.Scanln`.

- [ ] **Step 5: Build and test**

```bash
/usr/local/go/bin/go build ./...
/usr/local/go/bin/go test ./internal/cli/ -count=1
```

- [ ] **Step 6: Commit**

```bash
git add internal/cli/uninstall.go internal/cli/clean.go internal/cli/snapshot.go internal/cli/workspace.go
git commit -m "refactor: migrate uninstall/clean/snapshot/workspace prompts to huh"
```

---

## Task 4: Cleanup and lint

- [ ] **Step 1: Remove unused `bufio` imports across all modified files**

- [ ] **Step 2: Check for any remaining `scanner.Scan()` or `fmt.Scanln` calls**

```bash
grep -rn 'scanner\.Scan\|fmt\.Scan\|bufio\.NewScanner.*Stdin' internal/cli/*.go
```

The only remaining `bufio` usage should be in `start.go:306` (daemon mode stdin read — not a user prompt, internal IPC).

- [ ] **Step 3: Run linter**

```bash
PATH="/usr/local/go/bin:$PATH" ~/go/bin/golangci-lint run ./...
```

- [ ] **Step 4: Run full test suite**

```bash
/usr/local/go/bin/go test -race -timeout 120s ./internal/cli/ ./internal/cli/setup/ -count=1
```

- [ ] **Step 5: Commit fixes if any**

```bash
git add -A
git commit -m "chore: lint cleanup after huh migration"
```

---

## Key Rules

- `term.ReadPassword` stays for master password / password confirm — huh doesn't support this
- `--yes` flag (`flagYes`) must still bypass confirmations where it currently does
- `huh.ErrUserAborted` (Esc/Ctrl+C) returns the default value, not crash the wizard
- `printStepHeader` stays unchanged — it's output decoration, not input
- Welcome "Press Enter" can become a `huh.Confirm` with "Continue" label
- All functions that took `*bufio.Scanner` lose that parameter
- `start.go:306` (`bufio.NewReader(os.Stdin).ReadString('\n')`) is daemon IPC, NOT user interaction — don't touch it
- Import `"github.com/charmbracelet/huh"` in all modified files
- huh import is already in go.mod as a direct dependency
