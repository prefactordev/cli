package cli

import (
	"context"
	"net/http"
	"net/url"

	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/internal/api"
)

var (
	alertSeverityChoices = []string{"critical", "error", "warning", "info"}
	alertStatusChoices   = []string{"raised", "cleared"}
)

// checkAlertFilterChoices validates --status and --severity when they are set.
func checkAlertFilterChoices(cmd *cobra.Command) error {
	if flagSet(cmd, "status") {
		if err := checkChoice("--status <status>", stringFlag(cmd, "status"), alertStatusChoices); err != nil {
			return err
		}
	}
	if flagSet(cmd, "severity") {
		if err := checkChoice("--severity <severity>", stringFlag(cmd, "severity"), alertSeverityChoices); err != nil {
			return err
		}
	}
	return nil
}

func newAlertsCommand(app *App) *cobra.Command {
	alerts := &cobra.Command{Use: "alerts", Short: "Manage alerts"}

	list := &cobra.Command{
		Use:   "list",
		Short: "List alerts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := checkAlertFilterChoices(cmd); err != nil {
				return err
			}
			pagination, err := parsePaginationFlags(cmd)
			if err != nil {
				return err
			}

			params := &api.QueryAlertListParams{
				PaginationOffset:   pagination.Offset,
				PaginationPageSize: pagination.PageSize,
			}
			if value := stringFlag(cmd, "agent-instance-id"); value != "" {
				params.AgentInstanceId = &value
			}
			if value := stringFlag(cmd, "agent-id"); value != "" {
				params.AgentId = &value
			}
			if value := stringFlag(cmd, "environment-id"); value != "" {
				params.EnvironmentId = &value
			}
			if value := stringFlag(cmd, "status"); value != "" {
				status := api.QueryAlertListParamsStatus(value)
				params.Status = &status
			}
			if value := stringFlag(cmd, "severity"); value != "" {
				severity := api.QueryAlertListParamsSeverity(value)
				params.Severity = &severity
			}
			if value := stringFlag(cmd, "name"); value != "" {
				params.Name = &value
			}
			var sorting *string
			if value := stringFlag(cmd, "sorting"); value != "" {
				sorting = &value
			}

			// active_during values are sent as typed, not parsed as times.
			activeDuring := url.Values{}
			if value := stringFlag(cmd, "active-during-start-at"); value != "" {
				activeDuring.Set("active_during[start_at]", value)
			}
			if value := stringFlag(cmd, "active-during-finish-at"); value != "" {
				activeDuring.Set("active_during[finish_at]", value)
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryAlertList(ctx, params, withQuery(activeDuring), sortingQuery(sorting))
			})
		},
	}
	list.Flags().String("agent-instance-id", "", "Agent instance ID")
	list.Flags().String("agent-id", "", "Agent ID")
	list.Flags().String("environment-id", "", "Environment ID")
	list.Flags().String("status", "", "Alert status (raised, cleared)")
	list.Flags().String("severity", "", "Alert severity (critical, error, warning, info)")
	list.Flags().String("name", "", "Alert name")
	list.Flags().String("active-during-start-at", "", "Active during start time")
	list.Flags().String("active-during-finish-at", "", "Active during finish time")
	list.Flags().String("sorting", "", "Sorting")
	addPaginationFlags(list)
	alerts.AddCommand(list)

	alerts.AddCommand(&cobra.Command{
		Use:   "retrieve <id>",
		Short: "Retrieve alert",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryAlertGetDetails(ctx, args[0], &api.QueryAlertGetDetailsParams{})
			})
		},
	})

	count := &cobra.Command{
		Use:   "count",
		Short: "Count alerts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := checkAlertFilterChoices(cmd); err != nil {
				return err
			}

			params := &api.QueryAlertCountParams{}
			if value := stringFlag(cmd, "status"); value != "" {
				status := api.QueryAlertCountParamsStatus(value)
				params.Status = &status
			}
			if value := stringFlag(cmd, "severity"); value != "" {
				severity := api.QueryAlertCountParamsSeverity(value)
				params.Severity = &severity
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryAlertCount(ctx, params)
			})
		},
	}
	count.Flags().String("status", "", "Alert status (raised, cleared)")
	count.Flags().String("severity", "", "Alert severity (critical, error, warning, info)")
	alerts.AddCommand(count)

	raise := &cobra.Command{
		Use:   "raise",
		Short: "Raise an alert",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := checkChoice("--severity <severity>", stringFlag(cmd, "severity"), alertSeverityChoices); err != nil {
				return err
			}

			payload, err := parseJSONOption(stringFlag(cmd, "payload"), "--payload", "object")
			if err != nil {
				return err
			}

			body := map[string]any{
				"agent_instance_id": stringFlag(cmd, "agent-instance-id"),
				"name":              stringFlag(cmd, "name"),
				"severity":          stringFlag(cmd, "severity"),
				"payload":           payload,
			}
			optionalString(cmd, body, "raised-at", "raised_at")
			if boolFlag(cmd, "payload-sensitive-encoding") {
				body["payload_sensitive_encoding"] = true
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				reader, err := jsonBody(body)
				if err != nil {
					return nil, err
				}
				return c.ActionAlertRaiseWithBody(ctx, "application/json", reader)
			})
		},
	}
	raise.Flags().String("agent-instance-id", "", "Agent instance ID")
	raise.Flags().String("name", "", "Alert name")
	raise.Flags().String("severity", "", "Alert severity (critical, error, warning, info)")
	raise.Flags().String("payload", "", "JSON object or @file")
	raise.Flags().String("raised-at", "", "Raised at")
	raise.Flags().Bool("payload-sensitive-encoding", false, "Payload uses sensitive encoding")
	_ = raise.MarkFlagRequired("agent-instance-id")
	_ = raise.MarkFlagRequired("name")
	_ = raise.MarkFlagRequired("severity")
	_ = raise.MarkFlagRequired("payload")
	alerts.AddCommand(raise)

	clearCmd := &cobra.Command{
		Use:   "clear",
		Short: "Clear an alert",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if flagSet(cmd, "severity") {
				if err := checkChoice("--severity <severity>", stringFlag(cmd, "severity"), alertSeverityChoices); err != nil {
					return err
				}
			}

			body := map[string]any{
				"agent_instance_id": stringFlag(cmd, "agent-instance-id"),
				"name":              stringFlag(cmd, "name"),
			}
			if err := optionalJSONObject(cmd, body, "payload", "payload"); err != nil {
				return err
			}
			optionalString(cmd, body, "severity", "severity")
			optionalString(cmd, body, "cleared-at", "cleared_at")
			if boolFlag(cmd, "payload-sensitive-encoding") {
				body["payload_sensitive_encoding"] = true
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				reader, err := jsonBody(body)
				if err != nil {
					return nil, err
				}
				return c.ActionAlertClearWithBody(ctx, "application/json", reader)
			})
		},
	}
	clearCmd.Flags().String("agent-instance-id", "", "Agent instance ID")
	clearCmd.Flags().String("name", "", "Alert name")
	clearCmd.Flags().String("payload", "", "JSON object or @file")
	clearCmd.Flags().String("severity", "", "Alert severity (critical, error, warning, info)")
	clearCmd.Flags().String("cleared-at", "", "Cleared at")
	clearCmd.Flags().Bool("payload-sensitive-encoding", false, "Payload uses sensitive encoding")
	_ = clearCmd.MarkFlagRequired("agent-instance-id")
	_ = clearCmd.MarkFlagRequired("name")
	alerts.AddCommand(clearCmd)

	return alerts
}
