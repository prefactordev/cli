package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/internal/api"
)

// parseTeamIDs parses --team-ids as a JSON array of strings, or @file.
func parseTeamIDs(value string) (json.RawMessage, error) {
	raw, err := parseJSONOption(value, "--team-ids", "array")
	if err != nil {
		return nil, err
	}

	var items []any
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	for _, item := range items {
		if _, ok := item.(string); !ok {
			return nil, errors.New("--team-ids must be a JSON array of strings.")
		}
	}

	return raw, nil
}

func newPeopleCommand(app *App) *cobra.Command {
	people := &cobra.Command{Use: "people", Short: "Manage people"}

	list := &cobra.Command{
		Use:   "list",
		Short: "List people",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			q, err := parseSortingListFlags(cmd)
			if err != nil {
				return err
			}
			params := &api.QueryPersonListParams{PaginationOffset: q.Offset, PaginationPageSize: q.PageSize}
			if teamID := stringFlag(cmd, "team-id"); teamID != "" {
				params.TeamId = &teamID
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryPersonList(ctx, params, sortingQuery(q.Sorting))
			})
		},
	}
	list.Flags().String("team-id", "", "Team ID")
	addSortingListFlags(list)
	people.AddCommand(list)

	people.AddCommand(&cobra.Command{
		Use:   "retrieve <id>",
		Short: "Retrieve person",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryPersonGetDetails(ctx, args[0], &api.QueryPersonGetDetailsParams{})
			})
		},
	})

	create := &cobra.Command{
		Use:   "create",
		Short: "Create person",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			id := stringFlag(cmd, "id")
			if err := validateOptionalPfid(id, "--id"); err != nil {
				return err
			}

			details := map[string]any{
				"email": stringFlag(cmd, "email"),
				"name":  stringFlag(cmd, "name"),
			}
			if id != "" {
				details["id"] = id
			}
			if teamIDs := stringFlag(cmd, "team-ids"); teamIDs != "" {
				parsed, err := parseTeamIDs(teamIDs)
				if err != nil {
					return err
				}
				details["team_ids"] = parsed
			}
			if title := stringFlag(cmd, "title"); title != "" {
				details["title"] = title
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				body, err := jsonBody(map[string]any{"details": details})
				if err != nil {
					return nil, err
				}
				return c.ActionPersonCreateWithBody(ctx, "application/json", body)
			})
		},
	}
	create.Flags().String("email", "", "Email")
	create.Flags().String("name", "", "Name")
	create.Flags().String("id", "", "Person ID")
	create.Flags().String("team-ids", "", "JSON array of team IDs or @file")
	create.Flags().String("title", "", "Job title")
	_ = create.MarkFlagRequired("email")
	_ = create.MarkFlagRequired("name")
	people.AddCommand(create)

	update := &cobra.Command{
		Use:   "update <id>",
		Short: "Update person",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !flagSet(cmd, "email") && !flagSet(cmd, "name") && !flagSet(cmd, "team-ids") && !flagSet(cmd, "title") {
				return errors.New("No fields provided to update. Specify --email, --name, --team-ids, or --title.")
			}

			details := map[string]any{}
			for _, field := range []string{"email", "name"} {
				if flagSet(cmd, field) {
					details[field] = stringFlag(cmd, field)
				}
			}
			if flagSet(cmd, "team-ids") {
				parsed, err := parseTeamIDs(stringFlag(cmd, "team-ids"))
				if err != nil {
					return err
				}
				details["team_ids"] = parsed
			}
			if flagSet(cmd, "title") {
				details["title"] = stringFlag(cmd, "title")
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				body, err := jsonBody(map[string]any{"details": details})
				if err != nil {
					return nil, err
				}
				return c.ActionPersonUpdateWithBody(ctx, args[0], "application/json", body)
			})
		},
	}
	update.Flags().String("email", "", "Email")
	update.Flags().String("name", "", "Name")
	update.Flags().String("team-ids", "", "JSON array of team IDs or @file")
	update.Flags().String("title", "", "Job title")
	people.AddCommand(update)

	people.AddCommand(&cobra.Command{
		Use:   "delete <id>",
		Short: "Delete person",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.ActionPersonDeleteWithBody(ctx, args[0], "application/json", nil)
			})
		},
	})

	return people
}
