package cli

import (
	"context"
	"errors"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/internal/api"
)

// knowledgebaseStatusChoices are the --status values of knowledgebase and
// document list commands.
var knowledgebaseStatusChoices = []string{"active", "archived"}

// parseStatusFlag validates --status and returns it, or nil when it is not
// set.
func parseStatusFlag(cmd *cobra.Command) (*string, error) {
	if !flagSet(cmd, "status") {
		return nil, nil
	}

	status := stringFlag(cmd, "status")
	if err := checkChoice("--status <status>", status, knowledgebaseStatusChoices); err != nil {
		return nil, err
	}

	return &status, nil
}

func newKnowledgebasesCommand(app *App) *cobra.Command {
	knowledgebases := &cobra.Command{Use: "knowledgebases", Short: "Manage knowledgebases"}

	list := &cobra.Command{
		Use:   "list",
		Short: "List knowledgebases",
		Args:  cobra.NoArgs,
	}
	list.Flags().String("account-id", "", "Account ID")
	list.Flags().String("status", "", "Knowledgebase status")
	listFlags := addListFlags(list, "id", "name")
	list.RunE = func(cmd *cobra.Command, _ []string) error {
		status, err := parseStatusFlag(cmd)
		if err != nil {
			return err
		}
		query, err := listFlags.parse(cmd)
		if err != nil {
			return err
		}

		params := &api.QueryKnowledgebaseListParams{
			Sorting:            query.Sorting,
			PaginationOffset:   query.Offset,
			PaginationPageSize: query.PageSize,
		}
		if flagSet(cmd, "account-id") {
			accountID := stringFlag(cmd, "account-id")
			params.AccountId = &accountID
		}
		if status != nil {
			value := api.QueryKnowledgebaseListParamsStatus(*status)
			params.Status = &value
		}

		return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
			return c.QueryKnowledgebaseList(ctx, params)
		})
	}
	knowledgebases.AddCommand(list)

	knowledgebases.AddCommand(&cobra.Command{
		Use:   "retrieve <id>",
		Short: "Retrieve a knowledgebase",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryKnowledgebaseGetDetails(ctx, args[0], &api.QueryKnowledgebaseGetDetailsParams{})
			})
		},
	})

	create := &cobra.Command{
		Use:   "create",
		Short: "Create a knowledgebase",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			details := map[string]any{"name": stringFlag(cmd, "name")}
			if flagSet(cmd, "account-id") {
				details["account_id"] = stringFlag(cmd, "account-id")
			}
			if flagSet(cmd, "description") {
				details["description"] = stringFlag(cmd, "description")
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				body, err := jsonBody(map[string]any{"details": details})
				if err != nil {
					return nil, err
				}
				return c.ActionKnowledgebaseCreateWithBody(ctx, "application/json", body)
			})
		},
	}
	create.Flags().String("name", "", "Knowledgebase name")
	create.Flags().String("account-id", "", "Account ID")
	create.Flags().String("description", "", "Knowledgebase description")
	_ = create.MarkFlagRequired("name")
	knowledgebases.AddCommand(create)

	update := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a knowledgebase",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			hasName := flagSet(cmd, "name")
			hasDescription := flagSet(cmd, "description")
			clearDescription := boolFlag(cmd, "clear-description")
			if hasDescription && clearDescription {
				return errors.New("Use only one of --description or --clear-description.")
			}
			if !hasName && !hasDescription && !clearDescription {
				return errors.New("No fields provided to update. Specify --name, --description, or --clear-description.")
			}

			details := map[string]any{}
			if hasName {
				details["name"] = stringFlag(cmd, "name")
			}
			if clearDescription {
				details["description"] = nil
			} else if hasDescription {
				details["description"] = stringFlag(cmd, "description")
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				body, err := jsonBody(map[string]any{"details": details})
				if err != nil {
					return nil, err
				}
				return c.ActionKnowledgebaseUpdateWithBody(ctx, args[0], "application/json", body)
			})
		},
	}
	update.Flags().String("name", "", "Knowledgebase name")
	update.Flags().String("description", "", "Knowledgebase description")
	update.Flags().Bool("clear-description", false, "Clear the description")
	knowledgebases.AddCommand(update)

	knowledgebases.AddCommand(&cobra.Command{
		Use:   "archive <id>",
		Short: "Archive a knowledgebase",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.ActionKnowledgebaseArchiveWithBody(ctx, args[0], "application/json", nil)
			})
		},
	})

	knowledgebases.AddCommand(&cobra.Command{
		Use:   "unarchive <id>",
		Short: "Unarchive a knowledgebase",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.ActionKnowledgebaseUnarchiveWithBody(ctx, args[0], "application/json", nil)
			})
		},
	})

	knowledgebases.AddCommand(&cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a knowledgebase",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.ActionKnowledgebaseDeleteWithBody(ctx, args[0], "application/json", nil)
			})
		},
	})

	return knowledgebases
}
