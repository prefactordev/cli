package cli

import (
	"context"
	"errors"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/internal/api"
)

// addSortingListFlags registers the --sorting and pagination flags of list
// commands that take a raw sorting string.
func addSortingListFlags(cmd *cobra.Command) {
	cmd.Flags().String("sorting", "", "Sorting")
	addPaginationFlags(cmd)
}

// parseSortingListFlags parses --sorting, which is skipped when empty, and
// the pagination flags.
func parseSortingListFlags(cmd *cobra.Command) (listQuery, error) {
	q, err := parsePaginationFlags(cmd)
	if err != nil {
		return q, err
	}
	if sorting := stringFlag(cmd, "sorting"); sorting != "" {
		q.Sorting = &sorting
	}
	return q, nil
}

func newTeamsCommand(app *App) *cobra.Command {
	teams := &cobra.Command{Use: "teams", Short: "Manage teams"}

	list := &cobra.Command{
		Use:   "list",
		Short: "List teams",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			q, err := parseSortingListFlags(cmd)
			if err != nil {
				return err
			}
			params := &api.QueryTeamListParams{PaginationOffset: q.Offset, PaginationPageSize: q.PageSize}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryTeamList(ctx, params, sortingQuery(q.Sorting))
			})
		},
	}
	addSortingListFlags(list)
	teams.AddCommand(list)

	teams.AddCommand(&cobra.Command{
		Use:   "retrieve <id>",
		Short: "Retrieve team",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryTeamGetDetails(ctx, args[0], &api.QueryTeamGetDetailsParams{})
			})
		},
	})

	create := &cobra.Command{
		Use:   "create",
		Short: "Create team",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			id := stringFlag(cmd, "id")
			if err := validateOptionalPfid(id, "--id"); err != nil {
				return err
			}

			details := map[string]any{"name": stringFlag(cmd, "name")}
			if id != "" {
				details["id"] = id
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				body, err := jsonBody(map[string]any{"details": details})
				if err != nil {
					return nil, err
				}
				return c.ActionTeamCreateWithBody(ctx, "application/json", body)
			})
		},
	}
	create.Flags().String("name", "", "Team name")
	create.Flags().String("id", "", "Team ID")
	_ = create.MarkFlagRequired("name")
	teams.AddCommand(create)

	update := &cobra.Command{
		Use:   "update <id>",
		Short: "Update team",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !flagSet(cmd, "name") {
				return errors.New("No fields provided to update. Specify --name.")
			}

			details := map[string]any{"name": stringFlag(cmd, "name")}
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				body, err := jsonBody(map[string]any{"details": details})
				if err != nil {
					return nil, err
				}
				return c.ActionTeamUpdateWithBody(ctx, args[0], "application/json", body)
			})
		},
	}
	update.Flags().String("name", "", "Team name")
	teams.AddCommand(update)

	teams.AddCommand(&cobra.Command{
		Use:   "delete <id>",
		Short: "Delete team",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.ActionTeamDeleteWithBody(ctx, args[0], "application/json", nil)
			})
		},
	})

	return teams
}
