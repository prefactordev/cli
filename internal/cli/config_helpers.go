package cli

import (
	"io"
	"net/http"

	"github.com/spf13/cobra"
)

// jsonRequest encodes value as the request body and sends it with send.
func jsonRequest(value any, send func(body io.Reader) (*http.Response, error)) (*http.Response, error) {
	body, err := jsonBody(value)
	if err != nil {
		return nil, err
	}

	return send(body)
}

// parseListFlagsWithIDs parses the list flags of a list_from command in the
// TypeScript CLI's order: Commander's choice and pagination checks, then the
// ID list, then the --sort-direction requires --sort-field check.
func parseListFlagsWithIDs(cmd *cobra.Command, opts listOptions, idFlag string) ([]string, listQuery, error) {
	ids, idErr := parseIDList(stringFlag(cmd, idFlag), "--"+idFlag)

	q, err := opts.parse(cmd)
	if err != nil {
		directionOnly := flagSet(cmd, "sort-direction") && !flagSet(cmd, "sort-field") &&
			err.Error() == "--sort-direction requires --sort-field."
		if directionOnly && idErr != nil {
			return nil, q, idErr
		}
		return nil, q, err
	}
	if idErr != nil {
		return nil, q, idErr
	}

	return ids, q, nil
}
