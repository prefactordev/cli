package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/prefactordev/cli/internal/cli"
	"github.com/prefactordev/cli/internal/client"
	"github.com/prefactordev/cli/internal/output"
)

// version is set at build time with -ldflags "-X main.version=<version>".
// It must be a SemVer version, because the API rejects an X-Prefactor-SDK
// header without one.
var version = "0.0.0-dev"

func main() {
	if err := cli.NewRootCommand(version).Execute(); err != nil {
		printError(err)
		os.Exit(1)
	}
}

// printError writes the error message and, for an API error, the response
// body, as the TypeScript CLI does.
func printError(err error) {
	fmt.Fprintln(os.Stderr, err)

	var httpErr *client.HTTPError
	if !errors.As(err, &httpErr) || len(bytes.TrimSpace(httpErr.Body)) == 0 {
		return
	}

	if !json.Valid(httpErr.Body) {
		fmt.Fprintln(os.Stderr, string(httpErr.Body))
		return
	}

	_ = output.JSON(os.Stderr, httpErr.Body)
}
