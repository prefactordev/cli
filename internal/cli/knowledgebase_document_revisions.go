package cli

import (
	"context"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/internal/api"
)

func newKnowledgebaseDocumentRevisionsCommand(app *App) *cobra.Command {
	revisions := &cobra.Command{Use: "knowledgebase-document-revisions", Short: "Read knowledgebase document revisions"}

	list := &cobra.Command{
		Use:   "list",
		Short: "List revisions of a document",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pagination, err := parsePaginationFlags(cmd)
			if err != nil {
				return err
			}

			params := &api.QueryKnowledgebaseDocumentRevisionListParams{
				KnowledgebaseDocumentId: stringFlag(cmd, "knowledgebase-document-id"),
				PaginationOffset:        pagination.Offset,
				PaginationPageSize:      pagination.PageSize,
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryKnowledgebaseDocumentRevisionList(ctx, params)
			})
		},
	}
	list.Flags().String("knowledgebase-document-id", "", "Knowledgebase document ID")
	addPaginationFlags(list)
	_ = list.MarkFlagRequired("knowledgebase-document-id")
	revisions.AddCommand(list)

	retrieve := &cobra.Command{
		Use:   "retrieve <id>",
		Short: "Retrieve a document revision",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.retrieveContent(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryKnowledgebaseDocumentRevisionGetDetails(ctx, args[0], &api.QueryKnowledgebaseDocumentRevisionGetDetailsParams{})
			})
		},
	}
	retrieve.Flags().Bool("content-only", false, "Print revision content without the JSON envelope")
	revisions.AddCommand(retrieve)

	return revisions
}
