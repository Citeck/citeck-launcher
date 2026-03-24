package main

import "github.com/niceteck/citeck-launcher/internal/cli"

var version = "dev"

func main() {
	cli.Execute(version)
}
