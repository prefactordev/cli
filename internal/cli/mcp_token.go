package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/internal/api"
	"github.com/prefactordev/cli/internal/client"
	"github.com/prefactordev/cli/internal/profile"
)

// mcpTokenLifetime is how long a read-only token created for MCP lasts.
const mcpTokenLifetime = 90 * 24 * time.Hour

// mcpTokenRenewBefore is how close to its expiry a stored token is replaced
// rather than reused.
const mcpTokenRenewBefore = 7 * 24 * time.Hour

// mcpLockWait is how long a command waits for another prefactor command to
// finish changing the profiles file.
const mcpLockWait = 15 * time.Second

// mcpCredentials is the read-only token MCP clients send with --auth
// api-token.
type mcpCredentials struct {
	// profile is the profile the token belongs to.
	profile string
	baseURL string
	token   string
	id      string
	// expiresAt is the stored token's expiry, when known.
	expiresAt string
	// stored is set for a token kept in the profile for MCP, which the
	// CLI can replace when Prefactor refuses it.
	stored bool
	// created is set when this run created the token.
	created bool
	// direct is set when the token is the profile's own, used because no
	// read-only token could be created. It is used only when Prefactor
	// says it is read-only.
	direct bool
}

// where names the source of the token in messages.
func (c mcpCredentials) where() string {
	return fmt.Sprintf("profile '%s'", c.profile)
}

// mcpToken returns the profile's read-only token after checking with
// Prefactor that it is accepted and read-only. A stored token that
// Prefactor refuses, because it was revoked or has expired, is replaced
// once.
func (a *App) mcpToken(cmd *cobra.Command) (mcpCredentials, error) {
	ctx := cmd.Context()
	creds, err := a.readOnlyToken(ctx, cmd, "")
	if err != nil {
		return mcpCredentials{}, err
	}
	err = a.checkReadOnly(ctx, creds.baseURL, creds.token)
	if creds.stored && !creds.created && errors.Is(err, errTokenRefused) {
		if creds, err = a.readOnlyToken(ctx, cmd, creds.token); err != nil {
			return mcpCredentials{}, err
		}
		err = a.checkReadOnly(ctx, creds.baseURL, creds.token)
	}
	if err != nil {
		return mcpCredentials{}, mcpTokenError(err, creds)
	}
	return creds, nil
}

// mcpConfigPath returns the profiles file mcp commands use: --config when
// the command has it and it is set, or the usual one.
func (a *App) mcpConfigPath(cmd *cobra.Command) (string, error) {
	if f := cmd.Flags().Lookup("config"); f != nil && f.Value.String() != "" {
		return f.Value.String(), nil
	}
	return a.ConfigPath()
}

// readOnlyToken returns the read-only account API token MCP clients use
// for the selected profile. It never returns the profile's own token
// while a read-only one can be created; when one cannot, the caller must
// check that the profile's own token is read-only before using it.
//
// A token stored in the profile is reused until it is within 7 days of its
// expiry, or until it equals refused, the token Prefactor just refused
// because it was revoked or expired. Otherwise the profile's token creates a
// new read-only token that lasts 90 days, the profile stores it, and the
// token it replaces is revoked.
func (a *App) readOnlyToken(ctx context.Context, cmd *cobra.Command, refused string) (mcpCredentials, error) {
	path, err := a.mcpConfigPath(cmd)
	if err != nil {
		return mcpCredentials{}, err
	}
	name, isDefault := a.resolveProfileName(cmd)

	lockCtx, cancel := context.WithTimeout(ctx, mcpLockWait)
	defer cancel()
	unlock, err := profile.Lock(lockCtx, path)
	if err != nil {
		return mcpCredentials{}, err
	}
	defer unlock()

	store := profile.Load(path)
	p, ok := store.Get(name)
	if !ok {
		if a.Getenv("PREFACTOR_API_TOKEN") != "" && isDefault {
			return mcpCredentials{}, errors.New("prefactor mcp needs a saved profile to keep the read-only token in, because MCP clients do not see PREFACTOR_API_TOKEN. Run 'prefactor login' first.")
		}
		return mcpCredentials{}, fmt.Errorf("No profile found for '%s' in %s. Run 'prefactor login' first, or 'prefactor profiles add <name> [baseUrl] --api-token <apiToken>'.", name, path)
	}

	creds := mcpCredentials{profile: name, baseURL: p.BaseURL}
	if old := p.MCP; old != nil && old.Token != refused && !expiresSoon(old.ExpiresAt, a.now()) {
		creds.token, creds.id, creds.expiresAt, creds.stored = old.Token, old.ID, old.ExpiresAt, true
		return creds, nil
	}

	created, err := a.createReadOnlyToken(ctx, p)
	var httpErr *client.HTTPError
	if errors.As(err, &httpErr) && httpErr.Status == http.StatusForbidden {
		// The profile's token may not create tokens, for example because
		// it is itself read-only. mcpToken checks which.
		creds.token, creds.direct = p.APIKey, true
		return creds, nil
	}
	if errors.As(err, &httpErr) && httpErr.Status == http.StatusUnauthorized {
		return mcpCredentials{}, fmt.Errorf("Prefactor at %s refused the API token for %s, so no read-only token could be created. Run 'prefactor login' again, then retry.", creds.baseURL, creds.where())
	}
	if err != nil {
		return mcpCredentials{}, fmt.Errorf("Could not create a read-only API token for %s: %w", creds.where(), err)
	}

	replaced := p.MCP
	p.MCP = created
	if err := store.Set(name, p); err != nil {
		a.revokeToken(ctx, p, created.ID)
		return mcpCredentials{}, err
	}
	if replaced != nil && replaced.ID != "" && replaced.ID != created.ID {
		a.revokeToken(ctx, p, replaced.ID)
	}

	creds.token, creds.id, creds.expiresAt = created.Token, created.ID, created.ExpiresAt
	creds.stored, creds.created = true, true
	return creds, nil
}

// expiresSoon reports whether a token expiring at expiresAt should be
// replaced. An expiry that cannot be read leaves Prefactor to decide.
func expiresSoon(expiresAt string, now time.Time) bool {
	if expiresAt == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return false
	}
	return t.Sub(now) < mcpTokenRenewBefore
}

func (a *App) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// createReadOnlyToken creates a read-only account API token for the
// profile's account with the profile's token.
func (a *App) createReadOnlyToken(ctx context.Context, p profile.Profile) (*profile.MCPToken, error) {
	c, err := client.New(p.BaseURL, p.APIKey, a.Version)
	if err != nil {
		return nil, err
	}
	body, err := jsonBody(map[string]any{"details": map[string]any{
		"token_scope": "account",
		"role":        "read_only",
		"expires_at":  a.now().Add(mcpTokenLifetime).UTC().Format(time.RFC3339),
	}})
	if err != nil {
		return nil, err
	}
	raw, err := client.ReadJSON(c.ActionApiTokenCreateWithBody(ctx, "application/json", body))
	if err != nil {
		return nil, err
	}

	var resp struct {
		Token   string `json:"token"`
		Details struct {
			ID        string `json:"id"`
			Role      string `json:"role"`
			ExpiresAt string `json:"expires_at"`
		} `json:"details"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil || resp.Token == "" {
		return nil, errors.New("API token create response is missing token.")
	}
	if resp.Details.Role != "read_only" {
		// A Prefactor release without read-only tokens ignores the role.
		// Never keep a token that can change data.
		a.revokeToken(ctx, p, resp.Details.ID)
		return nil, fmt.Errorf("Prefactor at %s created a %q token instead of a read-only one, so it was revoked. This Prefactor release does not support read-only API tokens.", p.BaseURL, resp.Details.Role)
	}
	return &profile.MCPToken{Token: resp.Token, ID: resp.Details.ID, ExpiresAt: resp.Details.ExpiresAt}, nil
}

// revokeToken revokes an API token with the profile's token. It is best
// effort: a token that was already revoked or has expired cannot be.
func (a *App) revokeToken(ctx context.Context, p profile.Profile, id string) {
	if id == "" {
		return
	}
	c, err := client.New(p.BaseURL, p.APIKey, a.Version)
	if err != nil {
		return
	}
	_, _ = client.ReadJSON(c.ActionApiTokenRevokeWithBody(ctx, id, "application/json", emptyObject()))
}

var (
	// errTokenNotReadOnly is returned for a token that can change data.
	errTokenNotReadOnly = errors.New("the API token has full access")
	// errTokenRefused is returned for a token Prefactor does not accept.
	errTokenRefused = errors.New("Prefactor refused the API token")
	// errTokenUnchecked is returned when Prefactor could not be asked.
	errTokenUnchecked = errors.New("could not check the API token")
)

// checkReadOnly asks Prefactor whether token is accepted and read-only, as
// the MCP server does before it serves a request: a full-access token gets
// 403 there.
func (a *App) checkReadOnly(ctx context.Context, baseURL, token string) error {
	c, err := client.New(baseURL, token, a.Version)
	if err != nil {
		return err
	}
	raw, err := client.ReadJSON(c.QueryPingGet(ctx, &api.QueryPingGetParams{}))
	if err != nil {
		return tokenCheckError(err)
	}
	var ping struct {
		Details struct {
			APITokenID string `json:"api_token_id"`
			ReadOnly   *bool  `json:"read_only"`
		} `json:"details"`
	}
	if err := json.Unmarshal(raw, &ping); err != nil {
		return fmt.Errorf("%w: %w", errTokenUnchecked, err)
	}
	if ro := ping.Details.ReadOnly; ro != nil {
		if !*ro {
			return errTokenNotReadOnly
		}
		return nil
	}

	// Prefactor releases that do not report read_only on ping: read the
	// token's own record. A role that cannot be read counts as full access.
	if ping.Details.APITokenID == "" {
		return errTokenNotReadOnly
	}
	raw, err = client.ReadJSON(c.QueryApiTokenGetDetails(ctx, ping.Details.APITokenID, &api.QueryApiTokenGetDetailsParams{}))
	var httpErr *client.HTTPError
	if errors.As(err, &httpErr) && httpErr.Status == http.StatusUnauthorized {
		return errTokenRefused
	}
	var details struct {
		Details struct {
			Role string `json:"role"`
		} `json:"details"`
	}
	if err != nil || json.Unmarshal(raw, &details) != nil || details.Details.Role != "read_only" {
		return errTokenNotReadOnly
	}
	return nil
}

func tokenCheckError(err error) error {
	var httpErr *client.HTTPError
	if errors.As(err, &httpErr) && (httpErr.Status == http.StatusUnauthorized || httpErr.Status == http.StatusForbidden) {
		return errTokenRefused
	}
	return fmt.Errorf("%w: %w", errTokenUnchecked, err)
}

// mcpTokenError explains why a token cannot be given to MCP clients.
func mcpTokenError(err error, creds mcpCredentials) error {
	switch {
	case errors.Is(err, errTokenNotReadOnly) && creds.direct:
		return fmt.Errorf("The API token for %s has full access but cannot create a read-only token, and the MCP server only accepts a read-only token. Run 'prefactor login' again, or ask an account admin for a read-only API token.", creds.where())
	case errors.Is(err, errTokenNotReadOnly):
		return fmt.Errorf("Prefactor at %s says the token prefactor mcp created for %s has full access, so it was not used. Revoke it with 'prefactor api-tokens revoke %s'.", creds.baseURL, creds.where(), creds.id)
	case errors.Is(err, errTokenRefused):
		return fmt.Errorf("Prefactor at %s refused the API token for %s. Run 'prefactor login' again, then retry.", creds.baseURL, creds.where())
	case errors.Is(err, errTokenUnchecked):
		return fmt.Errorf("Could not reach Prefactor at %s to check the API token for %s: %w", creds.baseURL, creds.where(), errors.Unwrap(err))
	}
	return err
}
