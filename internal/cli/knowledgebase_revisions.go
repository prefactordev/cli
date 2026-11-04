package cli

import (
	"context"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/internal/api"
)

func newKnowledgebaseRevisionsCommand(app *App) *cobra.Command {
	revisions := &cobra.Command{Use: "knowledgebase-revisions", Short: "Read knowledgebase revisions"}

	list := &cobra.Command{
		Use:   "list",
		Short: "List revisions of a knowledgebase",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pagination, err := parsePaginationFlags(cmd)
			if err != nil {
				return err
			}

			params := &api.QueryKnowledgebaseRevisionListParams{
				KnowledgebaseId:    stringFlag(cmd, "knowledgebase-id"),
				PaginationOffset:   pagination.Offset,
				PaginationPageSize: pagination.PageSize,
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryKnowledgebaseRevisionList(ctx, params)
			})
		},
	}
	list.Flags().String("knowledgebase-id", "", "Knowledgebase ID")
	addPaginationFlags(list)
	_ = list.MarkFlagRequired("knowledgebase-id")
	revisions.AddCommand(list)

	revisions.AddCommand(&cobra.Command{
		Use:   "retrieve <id>",
		Short: "Retrieve a knowledgebase revision",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryKnowledgebaseRevisionGetDetails(ctx, args[0], &api.QueryKnowledgebaseRevisionGetDetailsParams{})
			})
		},
	})

	listDocuments := &cobra.Command{
		Use:   "list-documents <id>",
		Short: "List documents in a knowledgebase revision",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pagination, err := parsePaginationFlags(cmd)
			if err != nil {
				return err
			}

			params := &api.QueryKnowledgebaseRevisionListDocumentsParams{
				PaginationOffset:   pagination.Offset,
				PaginationPageSize: pagination.PageSize,
			}
			if flagSet(cmd, "path-prefix") {
				pathPrefix := stringFlag(cmd, "path-prefix")
				params.PathPrefix = &pathPrefix
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryKnowledgebaseRevisionListDocuments(ctx, args[0], params)
			})
		},
	}
	listDocuments.Flags().String("path-prefix", "", "Only documents whose path starts with this prefix")
	addPaginationFlags(listDocuments)
	revisions.AddCommand(listDocuments)

	return revisions
}
