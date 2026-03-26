package main

import "github.com/citeck/citeck-launcher/internal/cli"

var version = "dev"

func main() {
	cli.Execute(version)
}
