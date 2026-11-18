package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/internal/api"
	"github.com/prefactordev/cli/internal/client"
	"github.com/prefactordev/cli/internal/human"
	"github.com/prefactordev/cli/internal/langfuse"
	"github.com/prefactordev/cli/internal/profile"
)

const (
	// observationsPerPage keeps a page with input and output under
	// Langfuse Cloud's 5 MB response limit: one observation can reach
	// 70 KB.
	observationsPerPage = langfuse.ObservationsPerPage
	// tracesPerBatch is how many traces one imports/add_traces request
	// carries, as Prefactor suggests.
	tracesPerBatch = 20
	// maxBatchBytes keeps a request under Prefactor's 8,000,000 byte body
	// limit, with room to spare.
	maxBatchBytes = 7_900_000
	// uploadTimeout is the time limit for one batch, which can be several
	// megabytes.
	uploadTimeout = 2 * time.Minute
	// maxPrefactorRateLimitRetries is how many 429 responses one batch
	// waits out.
	maxPrefactorRateLimitRetries = 10
	maxPrefactorRetryAfter       = 2 * time.Minute
	// tooLarge is the reason given for a trace the CLI cannot send because
	// it alone is over the body limit.
	tooLarge = "too_large"
)

// prefactorTarget is where an import goes: the signed-in API and the
// account.
type prefactorTarget struct {
	baseURL      string
	client       *api.Client
	uploadClient *api.Client
	accountID    string
}

// langfuseImportResult is what import langfuse prints at the end.
type langfuseImportResult struct {
	ImportID  string           `json:"import_id"`
	AccountID string           `json:"account_id"`
	Status    string           `json:"status"`
	Window    langfuseWindow   `json:"window"`
	Agents    []langfuse.Agent `json:"agents"`
	Runs      importCounts     `json:"runs"`
	Rejected  []rejectedTrace  `json:"rejected"`
	AgentsURL string           `json:"agents_url"`
	NextStep  string           `json:"next_step"`
}

// importCounts are the runs sent and what Prefactor did with them.
type importCounts struct {
	Sent     int `json:"sent"`
	Written  int `json:"written"`
	Skipped  int `json:"skipped"`
	Rejected int `json:"rejected"`
}

// rejectedTrace is a run Prefactor, or the CLI, did not import, and why.
type rejectedTrace struct {
	TraceID string `json:"trace_id"`
	Reason  string `json:"reason"`
}

// traceResult is Prefactor's answer for one trace.
type traceResult struct {
	TraceID string  `json:"trace_id"`
	Status  string  `json:"status"`
	Reason  *string `json:"reason"`
}

const setupNextStep = "To send new runs to Prefactor, instrument the agent with the Prefactor SDK: run 'prefactor setup' for the values it needs."

// prefactorTarget signs in to Prefactor when there is no profile, then
// chooses the account to import into.
func (a *App) prefactorTarget(cmd *cobra.Command) (*prefactorTarget, error) {
	baseURL, token, err := a.prefactorCredentials(cmd)
	if err != nil {
		return nil, err
	}
	c, err := client.New(baseURL, token, a.Version)
	if err != nil {
		return nil, err
	}
	upload, err := client.NewForUploads(baseURL, token, a.Version, uploadTimeout)
	if err != nil {
		return nil, err
	}
	target := &prefactorTarget{baseURL: baseURL, client: c, uploadClient: upload}
	if target.accountID, err = a.chooseImportAccount(cmd, c); err != nil {
		return nil, err
	}
	return target, nil
}

// prefactorCredentials returns the selected profile's base URL and token.
// With the default profile and no token, it signs in through the browser
// first, as prefactor login does.
func (a *App) prefactorCredentials(cmd *cobra.Command) (string, string, error) {
	baseURL, token, err := a.credentials(cmd)
	if err == nil {
		return baseURL, token, nil
	}
	if _, isDefault := a.resolveProfileName(cmd); !isDefault {
		return "", "", err
	}
	status := cmd.ErrOrStderr()
	fmt.Fprintln(status, "You are not signed in to Prefactor, so the CLI will sign you in first. Your Langfuse keys stay on this machine.")
	grant, baseURL, err := a.signIn(cmd.Context(), status)
	if err != nil {
		return "", "", err
	}
	if grant.AccountName != "" {
		fmt.Fprintf(status, "Signed in to %s. Credentials saved to the '%s' profile.\n", grant.AccountName, profile.DefaultName)
	} else {
		fmt.Fprintf(status, "Signed in. Credentials saved to the '%s' profile.\n", profile.DefaultName)
	}
	return baseURL, grant.AccessToken, nil
}

// chooseImportAccount is --account-id, the only account the token can
// reach, or the account a person picks. Environments are not chosen: the
// server maps each Langfuse environment to one.
func (a *App) chooseImportAccount(cmd *cobra.Command, c *api.Client) (string, error) {
	if id := stringFlag(cmd, "account-id"); id != "" {
		return id, nil
	}
	choices, _, err := listChoices(cmd.Context(), c, accountKind, "")
	if err != nil {
		return "", err
	}
	switch {
	case len(choices) == 0:
		return "", errors.New("No accounts found to import into.")
	case len(choices) == 1:
		fmt.Fprintf(cmd.ErrOrStderr(), "Importing into the account %s.\n", accountText(choices[0]))
		return choices[0].Value, nil
	case a.canPick():
		return a.Picker.Pick("Choose the account to import into", "", choices)
	}
	labels := make([]string, len(choices))
	for i, choice := range choices {
		labels[i] = accountText(choice)
	}
	return "", fmt.Errorf("You can import into %d accounts. Pass --account-id <id> to choose one: %s.", len(choices), strings.Join(labels, ", "))
}

// accountText is an account's name with its ID in brackets.
func accountText(choice Choice) string {
	name := strings.TrimSpace(strings.TrimSuffix(choice.Label, choice.Value))
	if name == "" || name == choice.Value {
		return choice.Value
	}
	return name + " (" + choice.Value + ")"
}

// importUpload is one run of the upload.
type importUpload struct {
	target    *prefactorTarget
	langfuse  *langfuse.Client
	importID  string
	traceName map[string]string
	from, to  time.Time
	counts    importCounts
	rejected  []rejectedTrace
	progress  *importProgress
	sleep     func(context.Context, time.Duration) error
	batch     [][]byte
	batchSize int
}

// uploadLangfuse opens an import, sends the plan's runs and finishes the
// import, as failed when the upload stops early.
func (a *App) uploadLangfuse(cmd *cobra.Command, target *prefactorTarget, lf *langfuse.Client, plan langfusePlan, from, to time.Time) error {
	status := cmd.ErrOrStderr()
	// Runs without a trace name cannot be read by name, and Prefactor
	// needs the name to choose the agent, so they are left out.
	names := map[string]string{}
	var traceNames []string
	expected := 0
	for _, agent := range plan.Agents {
		if agent.Name == "" {
			fmt.Fprintf(status, "Leaving out %s without a trace name: Prefactor needs the trace name to choose the agent.\n", plural(agent.Runs, "run"))
			continue
		}
		names[agent.Name] = agent.Name
		traceNames = append(traceNames, agent.Name)
		expected += agent.Runs
	}
	if len(traceNames) == 0 {
		return writeImportMessage(cmd, "There are no runs to import.")
	}

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	importID, err := openImport(ctx, target)
	if err != nil {
		return err
	}
	fmt.Fprintf(status, "Opened import %s.\n", importID)

	if a.importSleep == nil {
		a.importSleep = sleepContext
	}
	u := &importUpload{
		target: target, langfuse: lf, importID: importID, traceName: names,
		from: from, to: to, sleep: a.importSleep,
		progress: newImportProgress(status, a.styled(status), expected),
	}
	runErr := u.run(ctx, traceNames)
	u.progress.done()

	finishStatus := "finished"
	if runErr != nil {
		finishStatus = "failed"
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	finishErr := finishImport(finishCtx, target, importID, finishStatus)

	switch {
	case runErr != nil && ctx.Err() != nil && cmd.Context().Err() == nil:
		runErr = errors.New("The import was stopped. Run the same command again to carry on: runs already imported are skipped.")
	case runErr != nil:
		runErr = fmt.Errorf("%w\nThe import stopped early. Run the same command again to carry on: runs already imported are skipped.", runErr)
	}

	result := langfuseImportResult{
		ImportID:  importID,
		AccountID: target.accountID,
		Status:    finishStatus,
		Window:    plan.Window,
		Agents:    plan.Agents,
		Runs:      u.counts,
		Rejected:  u.rejected,
		AgentsURL: strings.TrimRight(target.baseURL, "/") + "/agents",
		NextStep:  setupNextStep,
	}
	if result.Rejected == nil {
		result.Rejected = []rejectedTrace{}
	}
	if err := a.printImportResult(cmd, result); err != nil {
		return err
	}
	if runErr != nil {
		return runErr
	}
	if finishErr != nil {
		return fmt.Errorf("All runs were sent, but the import could not be finished: %w", finishErr)
	}
	return nil
}

// run pages through the window newest first, sends each trace once it is
// complete, then reads again in full the traces cut off at the edge of
// the window.
func (u *importUpload) run(ctx context.Context, traceNames []string) error {
	assembler := langfuse.NewAssembler()
	cursor := ""
	for {
		page, err := u.langfuse.Observations(ctx, langfuse.ObservationQuery{
			TraceNames: traceNames, From: u.from, To: u.to, Limit: observationsPerPage, Cursor: cursor,
		})
		if err != nil {
			return err
		}
		for _, trace := range assembler.Add(page.Observations) {
			if err := u.queue(ctx, trace); err != nil {
				return err
			}
		}
		if page.Cursor == "" || page.Cursor == cursor {
			break
		}
		cursor = page.Cursor
	}

	complete, partial := assembler.Finish()
	for _, trace := range complete {
		if err := u.queue(ctx, trace); err != nil {
			return err
		}
	}
	for _, id := range partial {
		trace, err := u.langfuse.Trace(ctx, id, u.to, observationsPerPage)
		if err != nil {
			return err
		}
		if len(trace.Observations) == 0 {
			continue
		}
		if err := u.queue(ctx, trace); err != nil {
			return err
		}
	}
	return u.flush(ctx)
}

// queue adds a trace to the batch, sending the batch first when the trace
// would make it too long or too large.
func (u *importUpload) queue(ctx context.Context, trace langfuse.Trace) error {
	name, ok := u.traceName[trace.TraceName()]
	if !ok {
		name = trace.TraceName()
	}
	encoded, err := trace.Encode(name)
	if err != nil {
		return err
	}
	if batchBytes(1, len(encoded)) > maxBatchBytes {
		u.counts.Rejected++
		u.rejected = append(u.rejected, rejectedTrace{TraceID: trace.ID, Reason: tooLarge})
		u.progress.update(u.counts)
		return nil
	}
	if len(u.batch) == tracesPerBatch || batchBytes(len(u.batch)+1, u.batchSize+len(encoded)) > maxBatchBytes {
		if err := u.flush(ctx); err != nil {
			return err
		}
	}
	u.batch = append(u.batch, encoded)
	u.batchSize += len(encoded)
	return nil
}

// batchBytes is the size of a request body holding count traces of total
// bytes: {"traces":[...]} with commas between them.
func batchBytes(count, total int) int {
	return len(`{"traces":[]}`) + total + max(count-1, 0)
}

// flush sends the batch and counts the results.
func (u *importUpload) flush(ctx context.Context) error {
	if len(u.batch) == 0 {
		return nil
	}
	results, err := u.send(ctx, u.batch)
	if err != nil {
		return err
	}
	u.counts.Sent += len(u.batch)
	for _, r := range results {
		switch r.Status {
		case "written":
			u.counts.Written++
		case "skipped":
			u.counts.Skipped++
		default:
			u.counts.Rejected++
			reason := r.Status
			if r.Reason != nil {
				reason = *r.Reason
			}
			u.rejected = append(u.rejected, rejectedTrace{TraceID: r.TraceID, Reason: reason})
		}
	}
	u.batch, u.batchSize = nil, 0
	u.progress.update(u.counts)
	return nil
}

// send posts traces to imports/add_traces. It waits out 429 responses as
// retry_after_ms asks, and splits a batch the server finds too large.
func (u *importUpload) send(ctx context.Context, traces [][]byte) ([]traceResult, error) {
	body := make([]byte, 0, batchBytes(len(traces), 0)+sumLengths(traces))
	body = append(body, `{"traces":[`...)
	body = append(body, bytes.Join(traces, []byte(","))...)
	body = append(body, `]}`...)

	for attempt := 1; ; attempt++ {
		raw, err := client.ReadJSON(u.target.uploadClient.ActionImportAddTracesWithBody(ctx, u.importID, "application/json", bytes.NewReader(body)))
		var httpErr *client.HTTPError
		switch {
		case err == nil:
			var response struct {
				Results []traceResult `json:"results"`
			}
			if err := json.Unmarshal(raw, &response); err != nil {
				return nil, fmt.Errorf("Could not read Prefactor's response to a batch of runs: %w", err)
			}
			return response.Results, nil
		case errors.As(err, &httpErr) && httpErr.Status == http.StatusTooManyRequests && attempt <= maxPrefactorRateLimitRetries:
			if err := u.sleep(ctx, prefactorRetryAfter(httpErr.Body, attempt)); err != nil {
				return nil, err
			}
		case errors.As(err, &httpErr) && httpErr.Status == http.StatusRequestEntityTooLarge && len(traces) > 1:
			half := len(traces) / 2
			first, err := u.send(ctx, traces[:half])
			if err != nil {
				return nil, err
			}
			second, err := u.send(ctx, traces[half:])
			if err != nil {
				return nil, err
			}
			return append(first, second...), nil
		default:
			return nil, prefactorError(err)
		}
	}
}

func sumLengths(items [][]byte) int {
	total := 0
	for _, item := range items {
		total += len(item)
	}
	return total
}

// prefactorRetryAfter is the wait a 429 body asks for in retry_after_ms,
// or a growing delay when it has none.
func prefactorRetryAfter(body []byte, attempt int) time.Duration {
	var parsed struct {
		RetryAfterMS *float64 `json:"retry_after_ms"`
	}
	wait := time.Duration(attempt) * 5 * time.Second
	if json.Unmarshal(body, &parsed) == nil && parsed.RetryAfterMS != nil && *parsed.RetryAfterMS >= 0 {
		wait = time.Duration(*parsed.RetryAfterMS * float64(time.Millisecond))
	}
	return min(wait, maxPrefactorRetryAfter)
}

// prefactorError adds Prefactor's message to a failed request.
func prefactorError(err error) error {
	var httpErr *client.HTTPError
	if !errors.As(err, &httpErr) || len(httpErr.Body) == 0 {
		return err
	}
	var body struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(httpErr.Body, &body) == nil && body.Message != "" {
		return fmt.Errorf("%s: %s", httpErr.Message, body.Message)
	}
	return err
}

// openImport creates an import for the account and returns its ID.
func openImport(ctx context.Context, target *prefactorTarget) (string, error) {
	body, err := jsonBody(map[string]any{"details": map[string]any{"source": "langfuse", "account_id": target.accountID}})
	if err != nil {
		return "", err
	}
	raw, err := client.ReadJSON(target.client.ActionImportCreateWithBody(ctx, "application/json", body))
	if err != nil {
		return "", prefactorError(err)
	}
	var response struct {
		Details struct {
			ID string `json:"id"`
		} `json:"details"`
	}
	if json.Unmarshal(raw, &response) != nil || response.Details.ID == "" {
		return "", errors.New("Import create response is missing details.id.")
	}
	return response.Details.ID, nil
}

// finishImport closes the import as finished or failed.
func finishImport(ctx context.Context, target *prefactorTarget, importID, status string) error {
	body, err := jsonBody(map[string]any{"status": status})
	if err != nil {
		return err
	}
	_, err = client.ReadJSON(target.client.ActionImportFinishWithBody(ctx, importID, "application/json", body))
	return prefactorError(err)
}

func writeImportMessage(cmd *cobra.Command, message string) error {
	_, err := fmt.Fprintln(cmd.ErrOrStderr(), message)
	return err
}

// printImportResult prints the result as JSON, or as text for a person at
// a terminal.
func (a *App) printImportResult(cmd *cobra.Command, result langfuseImportResult) error {
	out := cmd.OutOrStdout()
	if !a.styled(out) {
		return a.printJSONValue(cmd, result)
	}
	t := human.DefaultTheme()
	var b strings.Builder
	title := "Langfuse import finished"
	if result.Status != "finished" {
		title = "Langfuse import stopped"
	}
	b.WriteString(t.Title.Render(title) + "\n\n")
	b.WriteString(importSummary(result) + "\n")
	if len(result.Rejected) > 0 {
		b.WriteString("\n" + rejectedSummary(result.Rejected) + "\n")
	}
	b.WriteString("\nSee the agents at " + t.Command.Render(result.AgentsURL) + "\n")
	b.WriteString(result.NextStep + "\n")
	_, err := io.WriteString(human.Writer(out, os.Environ()), b.String())
	return err
}

// importSummary says what happened to the runs read from Langfuse.
func importSummary(result langfuseImportResult) string {
	c := result.Runs
	return fmt.Sprintf("Runs: %d imported, %d skipped because they were already imported, %d rejected.", c.Written, c.Skipped, c.Rejected)
}

// rejectedReasons explains the reasons Prefactor and the CLI give.
var rejectedReasons = map[string]string{
	"trace_name_blank":   "the trace has no name, so no agent can be chosen",
	"agent_retired":      "the agent for the trace name is retired",
	"trace_time_missing": "the trace has no start time",
	"invalid_trace":      "Prefactor could not store the trace",
	tooLarge:             "the trace is larger than the 8 MB Prefactor accepts in one request",
}

// rejectedSummary counts rejected runs by reason, with up to three trace
// IDs for each.
func rejectedSummary(rejected []rejectedTrace) string {
	var reasons []string
	byReason := map[string][]string{}
	for _, r := range rejected {
		if _, ok := byReason[r.Reason]; !ok {
			reasons = append(reasons, r.Reason)
		}
		byReason[r.Reason] = append(byReason[r.Reason], r.TraceID)
	}
	lines := []string{"Rejected runs:"}
	for _, reason := range reasons {
		ids := byReason[reason]
		text := reason
		if explained, ok := rejectedReasons[reason]; ok {
			text = explained
		}
		shown := ids[:min(len(ids), 3)]
		more := ""
		if len(ids) > len(shown) {
			more = fmt.Sprintf(" and %d more", len(ids)-len(shown))
		}
		lines = append(lines, fmt.Sprintf("  %d: %s (trace %s%s)", len(ids), text, strings.Join(shown, ", "), more))
	}
	return strings.Join(lines, "\n")
}

// importProgress shows how many runs have been sent. At a terminal it
// redraws a bar on one line; otherwise it prints a line for each batch.
type importProgress struct {
	w        io.Writer
	terminal bool
	expected int
	drawn    bool
}

func newImportProgress(w io.Writer, terminal bool, expected int) *importProgress {
	return &importProgress{w: w, terminal: terminal, expected: expected}
}

func (p *importProgress) update(c importCounts) {
	handled := c.Written + c.Skipped + c.Rejected
	total := max(p.expected, handled)
	counts := fmt.Sprintf("%d imported, %d skipped, %d rejected", c.Written, c.Skipped, c.Rejected)
	if !p.terminal {
		fmt.Fprintf(p.w, "Sent %d of about %s (%s).\n", handled, plural(total, "run"), counts)
		return
	}
	const width = 30
	filled := 0
	if total > 0 {
		filled = handled * width / total
	}
	bar := strings.Repeat("#", filled) + strings.Repeat("-", width-filled)
	fmt.Fprintf(p.w, "\r[%s] %d/%d runs  %s", bar, handled, total, counts)
	p.drawn = true
}

// done ends the bar's line.
func (p *importProgress) done() {
	if p.drawn {
		fmt.Fprintln(p.w)
		p.drawn = false
	}
}
