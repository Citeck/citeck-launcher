package embedded

import _ "embed"

// H2ExportJar contains the embedded H2 export tool JAR.
//
//go:embed h2-export.jar
var H2ExportJar []byte
