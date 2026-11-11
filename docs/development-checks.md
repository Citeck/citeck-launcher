# Development checks

Run `make check` before committing. It covers Go race tests, coverage floors,
lint, reachable Go vulnerabilities, frontend tests and the production audit,
builds, and cross-platform compilation checks. Native macOS tests still require
a Mac; a Darwin cross-lint run does not execute them.

## Go toolchain and analysis tools

`go.mod` requires **Go 1.27.4** and is the source of truth for every CI/release
`actions/setup-go` step. To use exactly that version locally (including after a
previous shell exported an older `GOTOOLCHAIN`):

```sh
export GOTOOLCHAIN=go1.27.4
go version
make tools
make check
```

The `go` directive is a minimum, not a maximum: a newer installed Go may otherwise
be selected. After changing Go's minor version, update the analysis tools too;
recompiling an old analyzer alone does not teach it the new export-data format.
Current pins:

| Tool | Version | Pin location |
| --- | --- | --- |
| golangci-lint | v2.15.0 (Go 1.27 support since v2.13.0) | `Makefile`, `.github/workflows/test.yml` |
| deadcode / golang.org/x/tools | v0.52.0 | `scripts/ci/deadcode.sh` |
| govulncheck | v1.9.0 (uses x/tools v0.52.0) | `scripts/ci/govulncheck.sh` |

Keep `make tools` and the CI linter pin aligned. The vulnerability and dead-code
allowlists do not change just because the tools were upgraded: investigate any
new findings rather than silently adding exceptions.

The project's supported macOS baseline is **14 Sonoma**. The desktop package's
`packaging/macos/Info.plist` sets `LSMinimumSystemVersion` to `14.0`.
This is a project policy, not Go's own floor: Go 1.27 requires macOS 13 Ventura,
including for pure-Go server/daemon auto-update payloads. Native macOS `make`
targets and the standalone macOS release script set the **14.0** compile/link
target for CGO builds through
`scripts/macos-compiler.sh`: it appends the target after Wails' older
package-level CGO flags, including intermediate and final links. Existing CC/CXX
commands and arguments are retained; nested make does not wrap them twice.
Bare `go` commands outside these entry points do not acquire this environment.
Explicit custom `-extld` overrides remain the caller's responsibility.

## Frontend dependency audit

Use the pinned pnpm version from `web/package.json`:

```sh
cd web
pnpm install --frozen-lockfile
pnpm audit --prod
pnpm audit
```

The full audit includes development tools. A clean production audit alone does
not establish that Vitest, jsdom or ESLint dependencies are free of reported
advisories. Keep both audit ignore lists empty when an upgrade is available.

The package manifest sets patched minimum versions for Vite and Vitest. Scoped
pnpm overrides also keep these development dependency paths on patched versions:

| Parent | Dependency | Minimum version |
| --- | --- | --- |
| jsdom | undici | 7.30.1 |
| minimatch | brace-expansion | 5.0.12 |
| @babel/helper-compilation-targets | browserslist | 4.28.7 |
| browserslist | baseline-browser-mapping | 2.11.0 |
| eslint-plugin-react-hooks | zod | 4.6.6 |

These overrides stay within the parents' declared compatible ranges. When
updating a parent, check whether its dependency range makes an override
redundant, regenerate the lockfile with pnpm, and run the full audit, tests,
lint and production build before removing it. Preserve unrelated lockfile
updates when bringing an older dependency branch onto master.

## Supervisor process fixtures

`TestSupervisorRestartReSelectsBinary` waits for the respawned child's marker;
fake readiness does not prove that the child has executed its script yet.

The fixture shell uses `exec sleep`, so the long-lived process is the child
owned by the supervisor. A separate `sleep` can survive termination of its shell
and hold the test runner's output pipes open: assertions print `PASS`, but
`go test` then fails with `WaitDelay expired before I/O complete`. Verify the
command's exit status, not just the test's assertion output. This lifecycle can
be checked with repeated scoped race tests as well as the full package suite.

## Unix-socket test fixtures

Do not bind sockets beneath `t.TempDir()`: its test-name component plus the
standard macOS `TMPDIR` can exceed the roughly 104-byte socket path limit.
Using `os.MkdirTemp("", ...)` removes the test name but still inherits a possibly
long `TMPDIR`.

Use the package's short socket-directory fixture (`cliSocketDir`,
`daemonSocketDir`, `desktopSocketDir`, `attachSocketDir`). On Unix these create
unique owner-only directories directly under `/tmp`, without changing the
process environment or touching an installation's run directory. Keep listener
shutdown ahead of directory cleanup. Missing-socket tests also need short paths
so they cannot pass accidentally on an overlong-path error.

## JVM attach coverage on non-Linux hosts

The `internal/jvmattach` 70% coverage floor applies on macOS too. Public host
attach deliberately returns `ErrUnsupported` there, so Linux-only `Command`
tests cannot supply that coverage. Keep the portable trigger/listener and wire
protocol tests: use a fake proc tree, a real fixture socket and an explicit fake
signal callback. Never enable host attach or send a real SIGQUIT just to cover
those branches. Check this package with `go test -race -cover ./internal/jvmattach`.

## MCP private-storage test fixtures

Do not pass the root of `t.TempDir()` directly to `launchermcp.OpenStore` as its
private database directory. Its mode depends on the toolchain and process umask;
it may be `0755`, whereas the real MCP store correctly requires owner-only
permissions. Use a fresh explicitly `0700` child (`testStorePath` / `testPrivateDir`
in `internal/launchermcp`, `privateMCPStorePath` in daemon tests). SQL fixtures
created without `OpenStore` must also pre-create their database file as `0600`;
SQLite's default creation mode follows umask.

To exercise the permissive-umask case without changing the current shell:

```sh
(umask 0022; go test ./internal/launchermcp)
```

Keep the negative permission tests: production must still refuse an existing
non-private leaf, never silently chmod a caller's ancestor directory.

## CodeMirror themes and package identity

Keep one resolved copy of CodeMirror's state, view and language packages and
Lezer's highlighting packages. Compatible duplicate versions can silently split
facet and highlighting identities: the app requests dark mode, but the editor
still reads its dark-theme facet as false, leaving light gutters and blue text
on the app's dark background. A successful build does not detect this.

After regenerating frontend dependencies, review `pnpm dedupe` and run the
`CodeEditor` integration test. It checks the editor's actual dark-theme facet
through a dark → light → dark switch, rather than just checking the React prop.
Also inspect the editor in a browser with synthetic YAML: verify gutter and
syntax contrast, search, editing and theme switching. Never use real namespace
secrets in screenshots or test fixtures.
