package cli

import (
	"context"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/internal/api"
)

func newAgentSpansCommand(app *App) *cobra.Command {
	agentSpans := &cobra.Command{Use: "agent-spans", Short: "Manage agent spans"}

	list := &cobra.Command{
		Use:   "list",
		Short: "List agent spans",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			agentInstanceID := stringFlag(cmd, "agent-instance-id")
			startTime := stringFlag(cmd, "start-time")
			endTime := stringFlag(cmd, "end-time")
			params := &api.QueryAgentSpanListParams{
				AgentInstanceId: &agentInstanceID,
				StartTime:       &startTime,
				EndTime:         &endTime,
			}
			if boolFlag(cmd, "include-summaries") {
				includeSummaries := true
				params.IncludeSummaries = &includeSummaries
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryAgentSpanList(ctx, params)
			})
		},
	}
	list.Flags().String("agent-instance-id", "", "Agent instance ID")
	list.Flags().String("start-time", "", "Start time")
	list.Flags().String("end-time", "", "End time")
	list.Flags().Bool("include-summaries", false, "Include summaries")
	_ = list.MarkFlagRequired("agent-instance-id")
	_ = list.MarkFlagRequired("start-time")
	_ = list.MarkFlagRequired("end-time")
	agentSpans.AddCommand(list)

	retrieve := &cobra.Command{
		Use:   "retrieve <id>",
		Short: "Retrieve agent span",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			params := &api.QueryAgentSpanGetDetailsParams{}
			if boolFlag(cmd, "redacted") {
				redacted := true
				params.Redacted = &redacted
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryAgentSpanGetDetails(ctx, args[0], params)
			})
		},
	}
	retrieve.Flags().Bool("redacted", false, "Return redacted payloads")
	agentSpans.AddCommand(retrieve)

	create := &cobra.Command{
		Use:   "create",
		Short: "Create agent span",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			id := stringFlag(cmd, "id")
			if err := validateOptionalPfid(id, "--id"); err != nil {
				return err
			}
			if err := validateOptionalPfid(stringFlag(cmd, "parent-span-id"), "--parent-span-id"); err != nil {
				return err
			}

			payload, err := parseJSONOption(stringFlag(cmd, "payload"), "--payload", "object")
			if err != nil {
				return err
			}

			details := map[string]any{
				"agent_instance_id": stringFlag(cmd, "agent-instance-id"),
				"schema_name":       stringFlag(cmd, "schema-name"),
				"status":            stringFlag(cmd, "status"),
				"payload":           payload,
			}
			optionalString(cmd, details, "id", "id")
			optionalString(cmd, details, "parent-span-id", "parent_span_id")
			optionalString(cmd, details, "started-at", "started_at")
			optionalString(cmd, details, "finished-at", "finished_at")
			if err := optionalJSONObject(cmd, details, "result-payload", "result_payload"); err != nil {
				return err
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				body, err := jsonBody(map[string]any{"details": details})
				if err != nil {
					return nil, err
				}
				return c.ActionAgentSpanCreateWithBody(ctx, "application/json", body)
			})
		},
	}
	create.Flags().String("agent-instance-id", "", "Agent instance ID")
	create.Flags().String("payload", "", "JSON object or @file")
	create.Flags().String("schema-name", "", "Schema name")
	create.Flags().String("status", "", "Status")
	create.Flags().String("id", "", "Span ID")
	create.Flags().String("parent-span-id", "", "Parent span ID")
	create.Flags().String("started-at", "", "Started at")
	create.Flags().String("finished-at", "", "Finished at")
	create.Flags().String("result-payload", "", "JSON object or @file")
	_ = create.MarkFlagRequired("agent-instance-id")
	_ = create.MarkFlagRequired("payload")
	_ = create.MarkFlagRequired("schema-name")
	_ = create.MarkFlagRequired("status")
	agentSpans.AddCommand(create)

	finish := &cobra.Command{
		Use:   "finish <id>",
		Short: "Finish agent span",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]any{}
			optionalString(cmd, body, "timestamp", "timestamp")
			optionalString(cmd, body, "status", "status")
			if err := optionalJSONObject(cmd, body, "result-payload", "result_payload"); err != nil {
				return err
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				reader, err := jsonBody(body)
				if err != nil {
					return nil, err
				}
				return c.ActionAgentSpanFinishWithBody(ctx, args[0], "application/json", reader)
			})
		},
	}
	finish.Flags().String("timestamp", "", "Timestamp")
	finish.Flags().String("status", "", "Status")
	finish.Flags().String("result-payload", "", "JSON object or @file")
	agentSpans.AddCommand(finish)

	agentSpans.AddCommand(&cobra.Command{
		Use:   "discard-sensitive <id>",
		Short: "Discard sensitive payload data from an agent span",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.ActionAgentSpanDiscardSensitiveWithBody(ctx, args[0], "application/json", emptyObject())
			})
		},
	})

	return agentSpans
}
