package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/prefactordev/cli/internal/cli"
	"github.com/prefactordev/cli/internal/client"
	"github.com/prefactordev/cli/internal/human"
	"github.com/prefactordev/cli/internal/jsjson"
	"github.com/prefactordev/cli/internal/output"
)

// version is set at build time with -ldflags "-X main.version=<version>".
// It must be a SemVer version, because the API rejects an X-Prefactor-SDK
// header without one.
var version = "0.0.0-dev"

func main() {
	if err := cli.NewRootCommand(version).Execute(); err != nil {
		if code, ok := cli.ExitCode(err); ok {
			os.Exit(code)
		}
		printError(err)
		os.Exit(1)
	}
}

// printError writes the error message and, for an API error, the response
// body, as the TypeScript CLI does.
func printError(err error) {
	// A person at a terminal gets the message in red and the hint dimmed;
	// the text is the same.
	stderr, styled := cli.ErrorWriter(os.Stderr)
	theme := human.DefaultTheme()
	if styled {
		fmt.Fprintln(stderr, theme.Danger.Render("✗ "+err.Error()))
	} else {
		fmt.Fprintln(os.Stderr, err)
	}

	var usageErr *cli.UsageError
	if errors.As(err, &usageErr) {
		if styled {
			fmt.Fprintln(stderr, theme.Muted.Render("(run with --help for usage)"))
		} else {
			fmt.Fprintln(os.Stderr, "(run with --help for usage)")
		}
		return
	}

	var httpErr *client.HTTPError
	if !errors.As(err, &httpErr) || len(httpErr.Body) == 0 {
		return
	}

	// The TypeScript CLI prints a body that is not JSON, or is a JSON
	// string, as text, prints nothing for a JSON null, and prints other JSON
	// as JSON.stringify(body, null, 2).
	parsed, err := jsjson.Parse(httpErr.Body)
	if err != nil {
		fmt.Fprintln(os.Stderr, string(httpErr.Body))
		return
	}
	switch body := parsed.(type) {
	case nil:
	case string:
		fmt.Fprintln(os.Stderr, body)
	default:
		_ = output.JSON(os.Stderr, httpErr.Body)
	}
}
