//go:build !desktop

package embedded

// H2ExportJar is empty in non-desktop builds. Server mode does not support
// H2→SQLite migration (the Kotlin launcher was desktop-only), so the ~1 MB
// JAR is gated behind the `desktop` build tag to keep the server binary lean.
//
// Callers in server mode must not reach this path — the only caller
// (RunJarMigration) is guarded by config.IsDesktopMode() in daemon/server.go.
var H2ExportJar []byte
