# Phase 10: mTLS for Web UI + Production Hardening — COMPLETE

## Context

Launcher Web UI currently works only on localhost (127.0.0.1:8088). For remote server management, users need secure access to the Web UI. mTLS (mutual TLS with client certificates) is the gold standard: the server verifies client identity via certificate, no passwords or tokens involved.

Additionally, a production audit found 2 P0, 10 P1, and ~14 P2 bugs that need fixing (1 P1 overlaps with mTLS work in 10b).

## mTLS Design

**Key principle:** No CA. Each client cert is self-signed. Server trusts individual certs (stored as files in a directory). Private keys NEVER stored on server.

Go x509 note: root certs in `ClientCAs` pool don't require `IsCA: true` — Go's chain verification only checks `IsCA` for intermediate certs, not roots. Self-signed cert in pool is both leaf and root, so `ExtKeyUsage=ClientAuth` is the only required extension.

### Flow

1. During `citeck install` wizard (if user agrees to remote Web UI) or `citeck cert generate --name admin`:
   - Generate self-signed ECDSA P-256 client cert (CN=admin, ExtKeyUsage=ClientAuth, 1 year)
   - Save **only the .crt** (public cert) to `{confDir}/webui-ca/admin.crt`
   - Print **both cert and private key PEM** to console
   - Also save a PKCS12 (.p12) file with restricted permissions (0600) and print its path (for browser import)
   - User must save the private key — it's never stored on the server

2. If user loses the private key → `citeck cert generate --name admin` again:
   - Generates a new cert+key pair
   - Overwrites `webui-ca/admin.crt` with the new public cert
   - Prints new private key to console
   - Daemon auto-reloads trust pool (via `GetConfigForClient`, no restart needed)

3. Adding more users → `citeck cert generate --name alice`
   - Same flow, new file `webui-ca/alice.crt`

4. Revoking a user → `citeck cert revoke --name alice`
   - Deletes `webui-ca/alice.crt`
   - Daemon auto-reloads trust pool on next TLS handshake (dir mtime changed)

5. Server startup (non-localhost listen):
   - Loads all `.crt`/`.pem` files from `{confDir}/webui-ca/` into `x509.CertPool`
   - Configures `tls.Config{ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool}`
   - Auto-generates server cert in `{confDir}/webui-tls/` via existing `tlsutil.GenerateSelfSignedCert`
   - If webui-ca/ is empty → refuse to start TCP listener, log actionable error

6. Dynamic cert pool reload:
   - Use `tls.Config.GetConfigForClient` callback — returns fresh `tls.Config` with updated `ClientCAs` on each handshake
   - Inside callback: check dir mtime, reload pool only if changed (cache last mtime + pool)
   - `citeck cert generate/revoke` takes effect immediately, no daemon restart needed

7. CLI remote access:
   - `citeck --host remote:8088 --tls-cert admin.crt --tls-key admin.key status`
   - Auto-discovers cert from `CITECK_TLS_CERT`/`CITECK_TLS_KEY` env vars
   - Server cert trust: `--server-cert server.crt` to pin specific server cert (adds to TLS roots pool)
   - If on the same machine (confdir accessible): auto-loads `{confDir}/webui-tls/server.crt` as trusted root
   - `--insecure` as escape hatch (skips server cert verification entirely)

### Auth logic by listener type

| Listener | Condition | Auth |
|---|---|---|
| Unix socket | Always | None (local process only) |
| TCP localhost (127.0.0.1) | `listen: 127.0.0.1:8088` | None (default) |
| TCP non-localhost | `listen: 0.0.0.0:8088` etc. | mTLS required |

Bearer token mechanism removed entirely (never released). `TokenAuthMiddleware` deleted. `ServerConfig.Token` field removed from `DaemonConfig`.

### Directory structure
```
/opt/citeck/conf/
  webui-ca/           # trusted client certs (public only, no private keys)
    admin.crt
    alice.crt
  webui-tls/          # server cert+key for Web UI HTTPS
    server.crt
    server.key
  daemon.yml          # no auth fields (mTLS is automatic for non-localhost)
```

---

## Sub-Phase 10a: P0 Bug Fixes (Shutdown Safety)

**2 issues, ~2 files**

### 10a-1: Shutdown panic on closed eventCh
- **File:** `internal/namespace/runtime.go`, `Shutdown()` (~line 442)
- **Bug:** `Shutdown` closes `eventCh` without waiting for `appWg`/`reconcileWg`. Goroutines from `StartApp`/`RestartApp` can send on closed channel → panic
- **Fix:** Add `r.appWg.Wait()` and `r.reconcileWg.Wait()` before `close(r.eventCh)`

### 10a-2: Snapshot download goroutine not tracked in bgWg
- **File:** `internal/daemon/routes_p2.go` (~line 327)
- **Bug:** `go d.downloadAndImportSnapshot(...)` bare goroutine — not in `bgWg`. `doShutdown` closes Docker client while goroutine still runs
- **Fix:** Wrap in `d.bgWg.Add(1)` / `defer d.bgWg.Done()`, pass `d.bgCtx`

---

## Sub-Phase 10b: mTLS Infrastructure (TLS Utilities + Config)

**~5 files**

### 10b-1: New `internal/tlsutil/clientcert.go`
- `GenerateClientCert(certPath, cn string, days int) (certPEM, keyPEM []byte, err error)`
  - Self-signed ECDSA P-256, CN=name, ExtKeyUsage=ClientAuth (no IsCA, no BasicConstraints)
  - Returns PEM bytes for both cert and key
  - Writes ONLY certPEM to certPath via `fsutil.AtomicWriteFile`
  - Does NOT write keyPEM to disk
- `GeneratePKCS12(certPEM, keyPEM []byte, password string) ([]byte, error)`
  - Creates .p12 bundle for browser import
- `LoadCACertPool(dir string) (*x509.CertPool, int, error)`
  - Loads all .crt/.pem files from directory
  - Returns pool + count of loaded certs
  - Empty/missing dir → empty pool, no error

### 10b-2: Fix `internal/tlsutil/selfcert.go` crash-safety
- Write key first, cert second (cert presence = completion signal)
- Use `fsutil.AtomicWriteFile` for both

### 10b-3: Config paths — `internal/config/paths.go`
- Add `WebUICADir() string` → `{confDir}/webui-ca/`
- Add `WebUITLSDir() string` → `{confDir}/webui-tls/`

### 10b-4: CLI `citeck cert generate` command — `internal/cli/cert.go`
- `citeck cert generate --name admin [--days 365]`
  - Calls `tlsutil.GenerateClientCert`
  - Saves .crt to `{WebUICADir()}/{name}.crt`
  - Writes PKCS12 to temp file
  - Prints to console:
    ```
    Client certificate generated for "admin"

    Certificate saved to: /opt/citeck/conf/webui-ca/admin.crt
    PKCS12 file (for browser import): /tmp/admin.p12  (password: <random>)

    === PRIVATE KEY (save this — it will NOT be shown again) ===
    -----BEGIN EC PRIVATE KEY-----
    ...
    -----END EC PRIVATE KEY-----

    === CERTIFICATE ===
    -----BEGIN CERTIFICATE-----
    ...
    -----END CERTIFICATE-----
    ```
- `citeck cert list` — lists certs in webui-ca/ with CN, expiry
- `citeck cert revoke --name alice` — deletes alice.crt from webui-ca/

---

## Sub-Phase 10c: mTLS Server + Client Integration

**~6 files**

### 10c-1: Server-side mTLS — `internal/daemon/server.go`
- Add `isLocalhostAddr(addr)` helper
- At TCP listener setup (~line 422):
  - If localhost → current behavior (plain HTTP, no auth)
  - If non-localhost:
    - Load client CA pool from `config.WebUICADir()` via `tlsutil.LoadCACertPool`
    - If pool empty, log error: "No client certs in {dir}. Run: citeck cert generate --name admin"
    - Load/generate server cert from `config.WebUITLSDir()`
    - Create `tls.Config{ClientAuth: RequireAndVerifyClientCert, ClientCAs: pool}`
    - Wrap listener with `tls.NewListener`
    - Log: "Web UI listening on https://... with mTLS (N trusted client certs)"

### 10c-2: mTLS identity middleware — `internal/daemon/middleware.go`
- New `MTLSIdentityMiddleware` — extracts CN from `r.TLS.PeerCertificates[0]`, adds to context, logs
- Delete `TokenAuthMiddleware` entirely (never released)
- Remove `ServerConfig.Token` field from `internal/config/daemon.go`
- For non-localhost: apply `MTLSIdentityMiddleware`
- For localhost: no auth middleware

### 10c-3: CLI client TLS — `internal/client/transport.go`
- Add `TLSCert`, `TLSKey`, `ServerCert`, `Insecure` fields to `TransportConfig`
- In `NewHTTPClient` for TCP:
  - Load client cert pair (`TLSCert` + `TLSKey`) into `tls.Config.Certificates`
  - If `ServerCert` set: load it into custom `x509.CertPool` as `RootCAs` (pin server cert)
  - If `Insecure`: set `InsecureSkipVerify` (escape hatch)
  - If neither: try auto-load from `{confDir}/webui-tls/server.crt`
- Auto-discover client cert: check `CITECK_TLS_CERT`/`CITECK_TLS_KEY` env vars
- `BaseURL` returns `https://` when TLS configured

### 10c-4: CLI flags — `internal/cli/root.go`
- Add `--tls-cert`, `--tls-key`, `--server-cert`, `--insecure` persistent flags
- Pass through to `DetectTransport`

### 10c-5: Install wizard integration — `internal/cli/install.go`
- During `citeck install`, if user configures `listen` to non-localhost:
  - Auto-generate first client cert: `tlsutil.GenerateClientCert("admin", ...)`
  - Save .crt to `webui-ca/admin.crt`
  - Print cert+key PEM and PKCS12 path to console
  - Print clear instructions: "Import the PKCS12 file into your browser" + "Save the private key"

---

## Sub-Phase 10d: P1 Bug Fixes

**9 issues, ~8 files** (selfcert write order already fixed in 10b-2)

| # | File | Function | Fix |
|---|---|---|---|
| 4 | `runtime.go` | `waitForDeps` | Replace `statusCond` with channel-based notify (eliminates lock inversion) |
| 5 | `runtime.go` | `StopApp` | Re-lookup app pointer from `r.apps[name]` after re-acquiring lock |
| 6 | `runtime.go` | `RestartApp` | Use `context.Background()` with timeout for Docker stop, not `runCtx` |
| 7 | `routes.go`, `routes_p2.go` | Many handlers | Add `getRuntime()` helper with `configMu.RLock`, replace bare `d.runtime` |
| 8 | `server.go` | `doShutdown` | Separate timeouts: 10s for bgWg, runtime has own, 10s for HTTP drain |
| 9 | `routes_p2.go` | `handleImportSnapshot` | Set Content-Type before WriteHeader, or create `writeJSONStatus` helper |
| 10 | `middleware.go` | `RateLimitMiddleware` | Reduce maxEntries to 1000, early-exit scan, or background eviction goroutine |
| 11 | `acme/client.go` | Challenge server | Add ReadTimeout/WriteTimeout/IdleTimeout to HTTP server |
| 12 | `snapshot.go` | `validateVolumeSnapshotMeta` | Add `sanitizeFileName(vol.Name)` check |

---

## Sub-Phase 10e: P2 Fixes (Selected)

**~9 issues, ~7 files**

| # | File | Fix |
|---|---|---|
| 13 | `routes.go` | System dump ZIP — load config through struct, marshal to YAML (sanitize future sensitive fields) |
| 14 | `routes.go` | `handlePutConfig` — replace inline temp+rename with `fsutil.AtomicWriteFile` |
| 15 | `routes_p2.go` | Snapshot upload — add `http.MaxBytesReader(w, r.Body, 2<<30)` |
| 16 | `runtime.go` | `Stop()` — make blocking send to `cmdCh` (or separate `stopCh`) |
| 17 | `docker/client.go` | `WaitForContainerExit` — add ContainerInspect pre-check for already-exited |
| 18 | `Logs.tsx` | Debounce regex search input (300ms), limit pattern length to 200 chars |
| 19 | `Welcome.tsx` | Add error state, display load failures |
| 20 | `store.ts` | Reset SSE backoff on `onopen`, not on every message |
| 21 | `Config.tsx` | Add `beforeunload` warning when unsaved edits exist |

---

## Execution Order

```
10a (P0 bugs)  ──┐
                  ├──→ 10c (mTLS server+client) ──→ 10d (P1 bugs) ──→ 10e (P2 fixes)
10b (TLS utils) ─┘
```

10a and 10b can run in parallel. 10c depends on 10b. 10d can start after 10a (shares runtime.go).

## Verification

1. `go test -race ./internal/...` — all pass, no races
2. `citeck cert generate --name admin` — prints cert+key, saves .crt only
3. `citeck cert list` — shows admin cert with expiry
4. Deploy to server with `listen: 0.0.0.0:8088` — mTLS enforced
5. Browser with imported .p12 → dashboard loads
6. Browser without cert → TLS handshake rejected
7. `citeck --host server:8088 --tls-cert admin.crt --tls-key admin.key status` → works
8. `kill -TERM` daemon during snapshot download → clean exit, no panic
9. Playwright test: HTTPS access with client cert
