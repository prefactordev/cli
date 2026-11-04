package main

import (
	"fmt"
	"os"

	"github.com/prefactordev/cli/internal/cli"
)

// version is set at build time with -ldflags "-X main.version=<version>".
var version = "dev"

func main() {
	if err := cli.NewRootCommand(version).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
