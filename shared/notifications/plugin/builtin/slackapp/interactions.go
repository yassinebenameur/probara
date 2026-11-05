package slackapp

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/yassinebenameur/probara/shared/notifications/plugin"
)

// ActionAcknowledge is the action_id of the card's Acknowledge button.
const ActionAcknowledge = "probara_ack"

// MaxSkew is how far a request's X-Slack-Request-Timestamp may be from the
// server clock: older (or future) signed requests are replays.
const MaxSkew = 5 * time.Minute

// Signature errors. Neither says which part failed beyond this.
var (
	ErrSignature = errors.New("slack request signature does not verify")
	ErrStale     = errors.New("slack request timestamp outside the allowed skew")
)

// VerifySignature checks a request Slack signed with the app's signing
// secret (Slack's v0 scheme): X-Slack-Signature must be
// "v0=" + hex(HMAC-SHA256(secret, "v0:" + timestamp + ":" + body)) and
// X-Slack-Request-Timestamp within MaxSkew of now. body is the raw request
// body exactly as received.
func VerifySignature(secret string, header http.Header, body []byte, now time.Time) error {
	if secret == "" {
		return ErrSignature
	}
	tsRaw := strings.TrimSpace(header.Get("X-Slack-Request-Timestamp"))
	ts, err := strconv.ParseInt(tsRaw, 10, 64)
	if err != nil {
		return ErrSignature
	}
	skew := now.Sub(time.Unix(ts, 0))
	if skew > MaxSkew || skew < -MaxSkew {
		return ErrStale
	}
	got := strings.TrimSpace(header.Get("X-Slack-Signature"))
	want := Sign(secret, tsRaw, body)
	if !hmac.Equal([]byte(got), []byte(want)) {
		return ErrSignature
	}
	return nil
}

// Sign computes the v0 signature of body sent at timestamp (tests and
// VerifySignature).
func Sign(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("v0:" + timestamp + ":"))
	mac.Write(body)
	return "v0=" + hex.EncodeToString(mac.Sum(nil))
}

// AckValue is the Acknowledge button's value: the alert and the alert
// channel the card was posted on.
func AckValue(alertID, channelID string) string {
	return alertID + ":" + channelID
}

// ParseAckValue splits an AckValue.
func ParseAckValue(v string) (alertID, channelID string, ok bool) {
	alertID, channelID, ok = strings.Cut(v, ":")
	if !ok || alertID == "" || channelID == "" {
		return "", "", false
	}
	return alertID, channelID, true
}

// Interaction is the part of a Slack interaction payload the API acts on.
type Interaction struct {
	Type string
	// TeamID is the workspace the press came from.
	TeamID string
	// ChannelID is the Slack channel of the message whose button was
	// pressed.
	ChannelID   string
	UserID      string
	UserName    string
	ResponseURL string
	Actions     []Action
}

// Action is one pressed control.
type Action struct {
	ActionID string
	Value    string
}

// ParseInteraction decodes an interaction request body (form-encoded, its
// JSON in the "payload" field).
func ParseInteraction(body []byte) (Interaction, error) {
	form, err := url.ParseQuery(string(body))
	if err != nil {
		return Interaction{}, fmt.Errorf("decode interaction form: %w", err)
	}
	raw := form.Get("payload")
	if raw == "" {
		return Interaction{}, errors.New("interaction has no payload")
	}
	var p struct {
		Type string `json:"type"`
		Team *struct {
			ID string `json:"id"`
		} `json:"team"`
		User struct {
			ID       string `json:"id"`
			Username string `json:"username"`
			Name     string `json:"name"`
			TeamID   string `json:"team_id"`
		} `json:"user"`
		Channel *struct {
			ID string `json:"id"`
		} `json:"channel"`
		Container struct {
			ChannelID string `json:"channel_id"`
		} `json:"container"`
		ResponseURL string `json:"response_url"`
		Actions     []struct {
			ActionID string `json:"action_id"`
			Value    string `json:"value"`
		} `json:"actions"`
	}
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return Interaction{}, fmt.Errorf("decode interaction payload: %w", err)
	}
	in := Interaction{
		Type:        p.Type,
		UserID:      p.User.ID,
		UserName:    p.User.Username,
		ResponseURL: p.ResponseURL,
		ChannelID:   p.Container.ChannelID,
	}
	if in.UserName == "" {
		in.UserName = p.User.Name
	}
	if p.Team != nil {
		in.TeamID = p.Team.ID
	}
	if in.TeamID == "" {
		in.TeamID = p.User.TeamID
	}
	if p.Channel != nil && p.Channel.ID != "" {
		in.ChannelID = p.Channel.ID
	}
	for _, a := range p.Actions {
		in.Actions = append(in.Actions, Action{ActionID: a.ActionID, Value: a.Value})
	}
	return in, nil
}

// Outcomes of an Acknowledge press, worded by ReplyText.
const (
	OutcomeAcknowledged = "acknowledged"
	OutcomeAlready      = "already"
	OutcomeResolved     = "resolved"
	OutcomeRefused      = "refused"
	OutcomeFailed       = "failed"
)

// ReplyText is the ephemeral answer to the user who pressed Acknowledge.
func ReplyText(outcome, subject string) string {
	if subject == "" {
		subject = "this alert"
	} else {
		subject = "*" + escape(subject) + "*"
	}
	switch outcome {
	case OutcomeAcknowledged:
		return ":white_check_mark: You acknowledged " + subject + ". The card updates in a moment."
	case OutcomeAlready:
		return ":information_source: " + capitalizeFirst(subject) + " was already acknowledged."
	case OutcomeResolved:
		return ":information_source: " + capitalizeFirst(subject) + " has already resolved."
	case OutcomeRefused:
		return ":no_entry: This button does not belong to this Slack workspace's Probara channel."
	default:
		return ":warning: Probara could not acknowledge " + subject + ". Try again, or acknowledge it in Probara."
	}
}

func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// Responder answers an interaction through its response_url.
type Responder struct {
	HTTP *http.Client
}

// NewResponder returns a responder on the guarded notification transport.
func NewResponder() *Responder {
	return &Responder{HTTP: plugin.NewHTTPClient(3 * time.Second)}
}

// Reply posts text as an ephemeral message to the user who pressed the
// button. responseURL comes from the (verified) payload, and must still be a
// Slack https URL.
func (r *Responder) Reply(ctx context.Context, responseURL, text string) error {
	u, err := plugin.ParseHTTPSURL(responseURL, "response_url")
	if err != nil {
		return err
	}
	if !plugin.HostMatches(u.Host, "slack.com") {
		return errors.New("response_url must point at a slack.com host")
	}
	return plugin.PostJSON(ctx, r.HTTP, u.String(), map[string]any{
		"response_type":    "ephemeral",
		"replace_original": false,
		"text":             text,
	}, nil, "slack response_url")
}
