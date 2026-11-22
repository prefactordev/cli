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
	// LateObservations counts observations Langfuse returned after their
	// run had been sent, which the run does not include.
	LateObservations int    `json:"late_observations"`
	AgentsURL        string `json:"agents_url"`
	NextStep         string `json:"next_step"`
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
	if target.accountID, err = a.chooseImportAccount(cmd, c, baseURL); err != nil {
		return nil, err
	}
	return target, nil
}

// prefactorCredentials returns the selected profile's base URL and token.
// With the default profile and no token, it signs in through the browser
// first, as prefactor login does: at the default profile's base URL when
// the profile has one, otherwise at PREFACTOR_API_URL or the default URL.
func (a *App) prefactorCredentials(cmd *cobra.Command) (string, string, error) {
	baseURL, token, err := a.credentials(cmd)
	_, isDefault := a.resolveProfileName(cmd)
	if err == nil && (token != "" || !isDefault) {
		return baseURL, token, nil
	}
	if !isDefault {
		return "", "", err
	}
	status := cmd.ErrOrStderr()
	fmt.Fprintln(status, "You are not signed in to Prefactor, so the CLI will sign you in first. Your Langfuse keys stay on this machine.")
	if err == nil && a.Getenv("PREFACTOR_API_TOKEN") != "" {
		fmt.Fprintf(status, "PREFACTOR_API_TOKEN is set, but the default profile has no token, so the CLI signs in at the profile's URL instead of using it.\n")
	}
	if store, err := a.loadProfiles(); err == nil {
		fmt.Fprintf(status, "Signing in to %s.\n", signInBaseURL(store, a.Getenv))
	}
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
func (a *App) chooseImportAccount(cmd *cobra.Command, c *api.Client, baseURL string) (string, error) {
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
		fmt.Fprintf(cmd.ErrOrStderr(), "Importing into the account %s at %s.\n", choices[0].Label, baseURL)
		return choices[0].Value, nil
	case a.canPick():
		return a.Picker.Pick("Choose the account to import into", "", choices)
	}
	labels := make([]string, len(choices))
	for i, choice := range choices {
		labels[i] = choice.Label
	}
	return "", fmt.Errorf("You can import into %d accounts. Pass --account-id <id> to choose one: %s.", len(choices), strings.Join(labels, ", "))
}

// importUpload is one run of the upload.
type importUpload struct {
	target    *prefactorTarget
	langfuse  *langfuse.Client
	importID  string
	from, to  time.Time
	counts    importCounts
	rejected  []rejectedTrace
	progress  *importProgress
	sleep     func(context.Context, time.Duration) error
	batch     [][]byte
	batchSize int
	// late counts observations that arrived after their run was sent.
	late int
}

// uploadLangfuse opens an import, sends the plan's runs and finishes the
// import, as failed when the upload stops early.
func (a *App) uploadLangfuse(cmd *cobra.Command, target *prefactorTarget, lf *langfuse.Client, plan langfusePlan, from, to time.Time) error {
	status := cmd.ErrOrStderr()
	// Runs without a trace name cannot be read by name, and Prefactor
	// needs the name to choose the agent, so they are left out.
	var traceNames []string
	expected := 0
	for _, agent := range plan.Agents {
		if agent.Name == "" {
			fmt.Fprintf(status, "Leaving out %s without a trace name: Prefactor needs the trace name to choose the agent.\n", plural(agent.Runs, "run"))
			continue
		}
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
		target: target, langfuse: lf, importID: importID,
		from: from, to: to, sleep: a.importSleep,
		progress: &importProgress{w: status, expected: expected},
	}
	runErr := u.run(ctx, traceNames)
	interrupted := ctx.Err() != nil && cmd.Context().Err() == nil
	// A second Ctrl-C while the import is finished stops the CLI at once.
	stop()

	finishStatus := "finished"
	if runErr != nil {
		finishStatus = "failed"
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	finishErr := finishImport(finishCtx, target, importID, finishStatus)

	switch {
	case runErr != nil && interrupted:
		runErr = errors.New("The import was stopped. Run the same command again to carry on: runs already imported are skipped.")
	case runErr != nil:
		runErr = fmt.Errorf("%w\nThe import stopped early. Run the same command again to carry on: runs already imported are skipped.", runErr)
	}
	if runErr != nil && finishErr != nil {
		runErr = fmt.Errorf("%w\nThe import could not be marked as failed either, so it stays open in Prefactor: %w", runErr, finishErr)
	}

	resultStatus := finishStatus
	if finishErr != nil {
		resultStatus = "not_finished"
	}
	if u.late > 0 {
		fmt.Fprintf(status, "%s arrived after its run was sent, so it is not in Prefactor. This happens when an observation starts more than 5 minutes before its run.\n", plural(u.late, "observation"))
	}
	result := langfuseImportResult{
		ImportID:         importID,
		AccountID:        target.accountID,
		Status:           resultStatus,
		Window:           plan.Window,
		Agents:           plan.Agents,
		Runs:             u.counts,
		Rejected:         u.rejected,
		LateObservations: u.late,
		AgentsURL:        strings.TrimRight(target.baseURL, "/") + "/agents",
		NextStep:         setupNextStep,
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

// run pages through the window newest first and sends each trace once it
// is complete. Traces whose root is not in the window started before it,
// are not in the plan's counts, and are left out.
func (u *importUpload) run(ctx context.Context, traceNames []string) error {
	assembler := langfuse.NewAssembler()
	defer func() { u.late = assembler.Late() }()
	cursor := ""
	limit := observationsPerPage
	for {
		page, err := u.langfuse.Observations(ctx, langfuse.ObservationQuery{
			TraceNames: traceNames, From: u.from, To: u.to, Limit: limit, Cursor: cursor,
		})
		// A page of large observations can pass Langfuse's response
		// limit, so the same page is read again in smaller parts.
		if errors.Is(err, langfuse.ErrResponseTooLarge) && limit > 1 {
			limit /= 2
			continue
		}
		if err != nil {
			return err
		}
		limit = observationsPerPage
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

	for _, trace := range assembler.Finish() {
		if err := u.queue(ctx, trace); err != nil {
			return err
		}
	}
	return u.flush(ctx)
}

// queue adds a trace to the batch, sending the batch first when the trace
// would make it too long or too large.
func (u *importUpload) queue(ctx context.Context, trace langfuse.Trace) error {
	encoded, err := trace.Encode()
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
	results, err := u.send(ctx)
	if err != nil {
		return err
	}
	if len(results) != len(u.batch) {
		return fmt.Errorf("Prefactor answered for %d of %d runs in a batch, so it is not known whether the rest were imported.", len(results), len(u.batch))
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

// send posts the batch to imports/add_traces. It waits out 429 responses
// as retry_after_ms asks.
func (u *importUpload) send(ctx context.Context) ([]traceResult, error) {
	body := make([]byte, 0, batchBytes(len(u.batch), u.batchSize))
	body = append(body, `{"traces":[`...)
	body = append(body, bytes.Join(u.batch, []byte(","))...)
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
		default:
			return nil, prefactorError(err)
		}
	}
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
		shown := ids[:min(len(ids), 3)]
		more := ""
		if len(ids) > len(shown) {
			more = fmt.Sprintf(" and %d more", len(ids)-len(shown))
		}
		lines = append(lines, fmt.Sprintf("  %d: %s (trace %s%s)", len(ids), reason, strings.Join(shown, ", "), more))
	}
	return strings.Join(lines, "\n")
}

// importProgress prints a line for each batch sent.
type importProgress struct {
	w        io.Writer
	expected int
}

func (p *importProgress) update(c importCounts) {
	handled := c.Written + c.Skipped + c.Rejected
	fmt.Fprintf(p.w, "Sent %d of about %s (%d imported, %d skipped, %d rejected).\n",
		handled, plural(max(p.expected, handled), "run"), c.Written, c.Skipped, c.Rejected)
}
