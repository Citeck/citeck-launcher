//go:build desktop

package embedded

import _ "embed"

// H2ExportJar contains the embedded H2 export tool JAR.
// Only embedded in desktop builds (tagged `desktop`). Server binaries
// ship without the JAR because server mode has no H2→SQLite migration path.
//
//go:embed h2-export.jar
var H2ExportJar []byte
