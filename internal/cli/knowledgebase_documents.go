package cli

import (
	"context"
	"errors"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/internal/api"
)

const documentContentUsage = "Document content, or @path to read a text file. @file content is limited to 1 MiB."

// retrieveContent sends call and prints the response, or only its
// details.content when --content-only is set.
func (a *App) retrieveContent(cmd *cobra.Command, call apiCall) error {
	if !boolFlag(cmd, "content-only") {
		return a.runAPI(cmd, call)
	}
	// PREFACTOR_OUTPUT_FORMAT only sets the default, so an explicit
	// --content-only wins over it; --json and --fields conflict.
	if a.session != nil && a.session.jsonFlag {
		return errors.New("--content-only cannot be used with --json or --fields.")
	}

	raw, err := a.callAPI(cmd, call)
	if err != nil {
		return err
	}
	return printDocumentContent(cmd, raw)
}

// revisionBody encodes a document action body that starts with
// base_revision_id.
func revisionBody(cmd *cobra.Command, extra map[string]any) map[string]any {
	body := map[string]any{"base_revision_id": stringFlag(cmd, "base-revision-id")}
	for key, value := range extra {
		body[key] = value
	}
	return body
}

func newKnowledgebaseDocumentsCommand(app *App) *cobra.Command {
	documents := &cobra.Command{Use: "knowledgebase-documents", Short: "Manage knowledgebase documents"}

	list := &cobra.Command{
		Use:   "list",
		Short: "List documents in a knowledgebase",
		Args:  cobra.NoArgs,
	}
	list.Flags().String("knowledgebase-id", "", "Knowledgebase ID")
	list.Flags().String("path-prefix", "", "Only documents whose path starts with this prefix")
	list.Flags().String("status", "", "Document status")
	addListFlags(list)
	_ = list.MarkFlagRequired("knowledgebase-id")
	list.RunE = func(cmd *cobra.Command, _ []string) error {
		status := statusFlag(cmd)
		query, err := parseListFlags(cmd)
		if err != nil {
			return err
		}

		params := &api.QueryKnowledgebaseDocumentListParams{
			KnowledgebaseId:    stringFlag(cmd, "knowledgebase-id"),
			PaginationOffset:   query.Offset,
			PaginationPageSize: query.PageSize,
		}
		if flagSet(cmd, "path-prefix") {
			pathPrefix := stringFlag(cmd, "path-prefix")
			params.PathPrefix = &pathPrefix
		}
		if status != nil {
			value := api.QueryKnowledgebaseDocumentListParamsStatus(*status)
			params.Status = &value
		}

		return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
			return c.QueryKnowledgebaseDocumentList(ctx, params, sortingQuery(query.Sorting))
		})
	}
	documents.AddCommand(list)

	retrieve := &cobra.Command{
		Use:   "retrieve <id>",
		Short: "Retrieve a document",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.retrieveContent(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryKnowledgebaseDocumentGetDetails(ctx, args[0], &api.QueryKnowledgebaseDocumentGetDetailsParams{})
			})
		},
	}
	retrieve.Flags().Bool("content-only", false, "Print document content without the JSON envelope")
	documents.AddCommand(retrieve)

	create := &cobra.Command{
		Use:   "create",
		Short: "Create a document",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			content, err := readTextOption(stringFlag(cmd, "content"), "--content")
			if err != nil {
				return err
			}

			details := map[string]any{
				"knowledgebase_id": stringFlag(cmd, "knowledgebase-id"),
				"path":             stringFlag(cmd, "path"),
				"content":          content,
			}
			if flagSet(cmd, "content-type") {
				details["content_type"] = stringFlag(cmd, "content-type")
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				body, err := jsonBody(map[string]any{"details": details})
				if err != nil {
					return nil, err
				}
				return c.ActionKnowledgebaseDocumentCreateWithBody(ctx, "application/json", body)
			})
		},
	}
	create.Flags().String("knowledgebase-id", "", "Knowledgebase ID")
	create.Flags().String("path", "", "Document path")
	create.Flags().String("content", "", documentContentUsage)
	create.Flags().String("content-type", "", "MIME content type")
	_ = create.MarkFlagRequired("knowledgebase-id")
	_ = create.MarkFlagRequired("path")
	_ = create.MarkFlagRequired("content")
	documents.AddCommand(create)

	update := &cobra.Command{
		Use:   "update <id>",
		Short: "Update document content",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			hasContent := flagSet(cmd, "content")
			hasContentType := flagSet(cmd, "content-type")
			if !hasContent && !hasContentType {
				return errors.New("No fields provided to update. Specify --content or --content-type.")
			}

			details := map[string]any{}
			if hasContent {
				content, err := readTextOption(stringFlag(cmd, "content"), "--content")
				if err != nil {
					return err
				}
				details["content"] = content
			}
			if hasContentType {
				details["content_type"] = stringFlag(cmd, "content-type")
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				body, err := jsonBody(revisionBody(cmd, map[string]any{"details": details}))
				if err != nil {
					return nil, err
				}
				return c.ActionKnowledgebaseDocumentUpdateWithBody(ctx, args[0], "application/json", body)
			})
		},
	}
	update.Flags().String("base-revision-id", "", "Document revision this change is based on")
	update.Flags().String("content", "", documentContentUsage)
	update.Flags().String("content-type", "", "MIME content type")
	_ = update.MarkFlagRequired("base-revision-id")
	documents.AddCommand(update)

	rename := &cobra.Command{
		Use:   "rename <id>",
		Short: "Rename a document",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				body, err := jsonBody(revisionBody(cmd, map[string]any{"path": stringFlag(cmd, "path")}))
				if err != nil {
					return nil, err
				}
				return c.ActionKnowledgebaseDocumentRenameWithBody(ctx, args[0], "application/json", body)
			})
		},
	}
	rename.Flags().String("base-revision-id", "", "Document revision this change is based on")
	rename.Flags().String("path", "", "New document path")
	_ = rename.MarkFlagRequired("base-revision-id")
	_ = rename.MarkFlagRequired("path")
	documents.AddCommand(rename)

	archive := &cobra.Command{
		Use:   "archive <id>",
		Short: "Archive a document",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				body, err := jsonBody(revisionBody(cmd, nil))
				if err != nil {
					return nil, err
				}
				return c.ActionKnowledgebaseDocumentArchiveWithBody(ctx, args[0], "application/json", body)
			})
		},
	}
	archive.Flags().String("base-revision-id", "", "Document revision this change is based on")
	_ = archive.MarkFlagRequired("base-revision-id")
	documents.AddCommand(archive)

	unarchive := &cobra.Command{
		Use:   "unarchive <id>",
		Short: "Unarchive a document",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			extra := map[string]any{}
			if flagSet(cmd, "path") {
				extra["path"] = stringFlag(cmd, "path")
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				body, err := jsonBody(revisionBody(cmd, extra))
				if err != nil {
					return nil, err
				}
				return c.ActionKnowledgebaseDocumentUnarchiveWithBody(ctx, args[0], "application/json", body)
			})
		},
	}
	unarchive.Flags().String("base-revision-id", "", "Document revision this change is based on")
	unarchive.Flags().String("path", "", "Path to restore the document to")
	_ = unarchive.MarkFlagRequired("base-revision-id")
	documents.AddCommand(unarchive)

	documents.AddCommand(&cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a document",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.ActionKnowledgebaseDocumentDeleteWithBody(ctx, args[0], "application/json", nil)
			})
		},
	})

	return documents
}
