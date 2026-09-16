// Command bwt is a CLI for the Bing Webmaster Tools API.
package main

import (
	"os"
	"runtime/debug"

	"github.com/trip-clear/bing-webmaster-cli/internal/cli"
)

// version is overridden at build time: -ldflags "-X main.version=v1.0.0".
// `go install` cannot pass ldflags, so it falls back to the module version
// that the Go toolchain embeds in the binary.
var version = "dev"

func main() {
	os.Exit(cli.Execute(resolveVersion()))
}

func resolveVersion() string {
	if version != "dev" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return version
	}
	return info.Main.Version
}
