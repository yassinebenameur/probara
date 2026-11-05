package slackapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/yassinebenameur/probara/shared/netguard"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
)

// apiResponse is the envelope every Slack Web API method answers with: HTTP
// 200 and "ok": false for most failures.
type apiResponse struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error"`
	TS      string `json:"ts"`
	Channel string `json:"channel"`
}

// maxResponse bounds how much of a Slack answer is read.
const maxResponse = 1 << 20

// call POSTs body as JSON to the Web API method with the bot token, and
// classifies the answer: transport and HTTP failures as plugin.Post does,
// then the method's own "ok": false error (classifyError).
func (p *Plugin) call(ctx context.Context, token, method string, body any) (apiResponse, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return apiResponse{}, plugin.Permanent(fmt.Errorf("marshal slack %s payload: %w", method, err))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.base+"/"+method, bytes.NewReader(payload))
	if err != nil {
		return apiResponse{}, plugin.Permanent(fmt.Errorf("build slack %s request: %w", method, err))
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "Probara-Alerts/1.0")
	return do(p.httpClient, req, "slack "+method)
}

// do sends req and decodes the Web API envelope.
func do(client *http.Client, req *http.Request, service string) (apiResponse, error) {
	resp, err := client.Do(req)
	if err != nil {
		// Keep only the cause: *url.Error prints the request URL.
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		if errors.Is(err, netguard.ErrBlocked) {
			return apiResponse{}, plugin.Permanent(fmt.Errorf("post %s: %w", service, err))
		}
		return apiResponse{}, fmt.Errorf("post %s: %w", service, err)
	}
	defer resp.Body.Close()
	if err := plugin.CheckResponse(resp, service); err != nil {
		return apiResponse{}, err
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return apiResponse{}, fmt.Errorf("read %s response: %w", service, err)
	}
	var out apiResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return apiResponse{}, fmt.Errorf("%s returned an unreadable response", service)
	}
	if !out.OK {
		return out, classifyError(service, out.Error)
	}
	return out, nil
}

// permanentErrors are Web API errors that describe the channel's
// configuration or the card, not a passing condition: retrying fails the same
// way, so the alerter keeps its claim and the worker stops redelivering.
var permanentErrors = map[string]bool{
	"invalid_auth":                          true,
	"not_authed":                            true,
	"account_inactive":                      true,
	"token_revoked":                         true,
	"token_expired":                         true,
	"no_permission":                         true,
	"missing_scope":                         true,
	"not_allowed_token_type":                true,
	"team_access_not_granted":               true,
	"ekm_access_denied":                     true,
	"channel_not_found":                     true,
	"not_in_channel":                        true,
	"is_archived":                           true,
	"restricted_action":                     true,
	"restricted_action_read_only_channel":   true,
	"restricted_action_thread_only_channel": true,
	"invalid_blocks":                        true,
	"invalid_blocks_format":                 true,
	"invalid_arguments":                     true,
	"msg_too_long":                          true,
	"too_many_attachments":                  true,
	"no_text":                               true,
	"message_not_found":                     true,
	"cant_update_message":                   true,
	"edit_window_closed":                    true,
	"thread_not_found":                      true,
	"invalid_code":                          true,
	"bad_client_secret":                     true,
	"invalid_client_id":                     true,
	"bad_redirect_uri":                      true,
	"code_already_used":                     true,
}

// classifyError turns a Web API "ok": false error code into a delivery error.
// The code is Slack's machine-readable reason and carries no secret.
func classifyError(service, code string) error {
	if code == "" {
		code = "unknown_error"
	}
	err := fmt.Errorf("%s failed: %s", service, code)
	if permanentErrors[code] {
		return plugin.Permanent(err)
	}
	return err
}

// --- OAuth install --------------------------------------------------------

// InstallScopes are the bot scopes "Add to Slack" requests: chat:write to post
// and edit cards, chat:write.public to post in public channels the app has
// not joined, and incoming-webhook for Slack's channel picker during the
// install (the picked channel comes back with the token; the webhook URL
// itself is ignored).
const InstallScopes = "chat:write,chat:write.public,incoming-webhook"

// AuthorizeURL is the Slack consent page "Add to Slack" opens.
func AuthorizeURL(clientID, state, redirectURI string) string {
	q := url.Values{
		"client_id":    {clientID},
		"scope":        {InstallScopes},
		"state":        {state},
		"redirect_uri": {redirectURI},
	}
	return "https://slack.com/oauth/v2/authorize?" + q.Encode()
}

// Installation is what a completed OAuth install hands back: the bot token
// and where it may post.
type Installation struct {
	BotToken    string
	TeamID      string
	TeamName    string
	ChannelID   string
	ChannelName string
}

// OAuthClient exchanges an install's authorization code.
type OAuthClient struct {
	// BaseURL is the Web API root (APIBase); tests point it elsewhere.
	BaseURL string
	HTTP    *http.Client
}

// NewOAuthClient returns a client on the guarded notification transport.
func NewOAuthClient() *OAuthClient {
	return &OAuthClient{BaseURL: APIBase, HTTP: plugin.NewHTTPClient(10 * time.Second)}
}

// Exchange trades code for the installation (oauth.v2.access).
func (c *OAuthClient) Exchange(ctx context.Context, clientID, clientSecret, code, redirectURI string) (Installation, error) {
	form := url.Values{
		"code":         {code},
		"redirect_uri": {redirectURI},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/oauth.v2.access", strings.NewReader(form.Encode()))
	if err != nil {
		return Installation{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(clientID, clientSecret)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return Installation{}, fmt.Errorf("slack oauth.v2.access: %w", err)
	}
	defer resp.Body.Close()
	if err := plugin.CheckResponse(resp, "slack oauth.v2.access"); err != nil {
		return Installation{}, err
	}
	var out struct {
		Ok          bool   `json:"ok"`
		Error       string `json:"error"`
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		Team        struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"team"`
		IncomingWebhook struct {
			Channel   string `json:"channel"`
			ChannelID string `json:"channel_id"`
		} `json:"incoming_webhook"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponse)).Decode(&out); err != nil {
		return Installation{}, errors.New("slack oauth.v2.access returned an unreadable response")
	}
	if !out.Ok {
		return Installation{}, classifyError("slack oauth.v2.access", out.Error)
	}
	if out.TokenType != "" && out.TokenType != "bot" {
		return Installation{}, fmt.Errorf("slack oauth.v2.access returned a %s token, want bot", out.TokenType)
	}
	return Installation{
		BotToken:    out.AccessToken,
		TeamID:      out.Team.ID,
		TeamName:    out.Team.Name,
		ChannelID:   out.IncomingWebhook.ChannelID,
		ChannelName: strings.TrimPrefix(out.IncomingWebhook.Channel, "#"),
	}, nil
}
