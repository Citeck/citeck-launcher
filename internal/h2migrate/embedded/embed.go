package embedded

import _ "embed"

//go:embed h2-export.jar
var H2ExportJar []byte
