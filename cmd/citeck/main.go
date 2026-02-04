package main

import "github.com/citeck/citeck-launcher/internal/cli"

var (
	version   = "dev"
	gitCommit = ""
	buildDate = ""
)

func main() {
	cli.Execute(cli.BuildInfo{Version: version, Commit: gitCommit, BuildDate: buildDate})
}
