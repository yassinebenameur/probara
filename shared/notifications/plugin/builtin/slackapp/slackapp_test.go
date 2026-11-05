package slackapp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/internal/plugintest"
	"github.com/yassinebenameur/probara/shared/notifications/present"
)

const (
	token     = "xoxb-1-2-abc"
	channelID = "11111111-2222-3333-4444-555555555555"
)

func TestMain(m *testing.M) {
	plugintest.AllowLoopback()
	os.Exit(m.Run())
}

// fakeStore is an in-memory plugin.MessageStore.
type fakeStore struct {
	mu    sync.Mutex
	ref   json.RawMessage
	saves int
}

func (s *fakeStore) Load(context.Context) (json.RawMessage, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ref, s.ref != nil, nil
}

func (s *fakeStore) Save(_ context.Context, ref json.RawMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ref, s.saves = ref, s.saves+1
	return nil
}

// call is one Web API request the fake Slack received.
type call struct {
	Method string
	Auth   string
	Body   map[string]any
}

// fakeSlack answers every method with ok and a ts, or with answer when set.
func fakeSlack(t *testing.T, answer string) (*httptest.Server, *[]call) {
	t.Helper()
	var mu sync.Mutex
	calls := &[]call{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		*calls = append(*calls, call{Method: strings.TrimPrefix(r.URL.Path, "/"), Auth: r.Header.Get("Authorization"), Body: body})
		mu.Unlock()
		if answer != "" {
			_, _ = w.Write([]byte(answer))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"channel":"C0PROBARA","ts":"1700000000.000100"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, calls
}

func newPlugin(base string) *Plugin {
	p := New()
	p.base = base
	return p
}

func request(eventType string, cfg map[string]any, store plugin.MessageStore) plugin.DispatchRequest {
	ev := plugintest.SampleEvent()
	ev.Type = eventType
	if eventType == present.EventResolved {
		ev.Alert.Status = "resolved"
	}
	if cfg == nil {
		cfg = map[string]any{"bot_token": token, "team_id": "T0TEAM", "channel_id": "C0PROBARA"}
	}
	return plugin.DispatchRequest{
		Channel:   plugin.ChannelRef{ID: channelID, Name: "slack", Config: cfg},
		Event:     ev,
		EventType: eventType,
		Attempt:   1,
		Messages:  store,
	}
}

func blocksJSON(t *testing.T, c call) string {
	t.Helper()
	raw, err := json.Marshal(c.Body["blocks"])
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestManifest(t *testing.T) {
	m := New().Manifest()
	if m.Type != "slack_app" {
		t.Fatalf("Type = %q", m.Type)
	}
	for _, c := range []plugin.Capability{plugin.CapabilityTestable, plugin.CapabilityFollowUp, plugin.CapabilityInteractive, plugin.CapabilityMessageUpdate} {
		if !m.HasCapability(c) {
			t.Errorf("missing capability %q", c)
		}
	}
	if m.HasCapability(plugin.CapabilityAcknowledge) {
		t.Error("a chat card follows acknowledgements through updates, not the paging acknowledged event")
	}
	plugintest.AssertSecrets(t, New(), map[string]any{
		"bot_token": token, "team_id": "T0TEAM", "channel_id": "C0PROBARA", "interactive": true,
	}, "bot_token")
}

func TestValidate(t *testing.T) {
	cases := []struct {
		raw     string
		wantErr bool
	}{
		{`{"bot_token":"xoxb-1","team_id":"T012AB","channel_id":"C012AB"}`, false},
		{`{"bot_token":"xoxe.xoxb-1-2","team_id":"E012AB","channel_id":"G012AB","interactive":false}`, false},
		{`{"bot_token":"xoxp-user","team_id":"T012AB","channel_id":"C012AB"}`, true},
		{`{"bot_token":"xoxb-1","team_id":"acme","channel_id":"C012AB"}`, true},
		{`{"bot_token":"xoxb-1","team_id":"T012AB","channel_id":"#alerts"}`, true},
		{`{}`, true},
		{``, true},
	}
	for _, tc := range cases {
		if err := New().Validate(json.RawMessage(tc.raw)); (err != nil) != tc.wantErr {
			t.Errorf("Validate(%s) err=%v wantErr=%v", tc.raw, err, tc.wantErr)
		}
	}
}

// The trigger posts a card with an in-Slack Acknowledge button carrying the
// alert and channel, and saves the card's ts.
func TestCreatedPostsCardAndSavesRef(t *testing.T) {
	srv, calls := fakeSlack(t, "")
	store := &fakeStore{}
	if err := newPlugin(srv.URL).Send(context.Background(), request(present.EventCreated, nil, store)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(*calls) != 1 || (*calls)[0].Method != "chat.postMessage" {
		t.Fatalf("calls = %+v", *calls)
	}
	c := (*calls)[0]
	if c.Auth != "Bearer "+token {
		t.Fatalf("Authorization = %q", c.Auth)
	}
	if c.Body["channel"] != "C0PROBARA" || c.Body["thread_ts"] != nil {
		t.Fatalf("body = %v", c.Body)
	}
	blocks := blocksJSON(t, c)
	if !strings.Contains(blocks, `"action_id":"probara_ack"`) || !strings.Contains(blocks, `"value":"a-1:`+channelID+`"`) {
		t.Fatalf("card has no acknowledge button: %s", blocks)
	}
	if store.saves != 1 || string(store.ref) != `{"channel":"C0PROBARA","ts":"1700000000.000100"}` {
		t.Fatalf("saved %d refs: %s", store.saves, store.ref)
	}
}

// Reminders and the resolve go as replies in the card's thread, without a
// second Acknowledge button; with no card saved they become the card.
func TestFollowUpsThreadUnderTheCard(t *testing.T) {
	srv, calls := fakeSlack(t, "")
	store := &fakeStore{ref: json.RawMessage(`{"channel":"C0PROBARA","ts":"1.2"}`)}
	p := newPlugin(srv.URL)
	for _, ev := range []string{present.EventReminder, present.EventResolved} {
		if err := p.Send(context.Background(), request(ev, nil, store)); err != nil {
			t.Fatalf("%s: %v", ev, err)
		}
	}
	for _, c := range *calls {
		if c.Method != "chat.postMessage" || c.Body["thread_ts"] != "1.2" {
			t.Fatalf("follow-up not threaded: %+v", c)
		}
		if strings.Contains(blocksJSON(t, c), "probara_ack") {
			t.Fatalf("thread reply repeats the acknowledge button: %s", blocksJSON(t, c))
		}
	}
	if store.saves != 0 {
		t.Fatalf("a thread reply replaced the card (%d saves)", store.saves)
	}

	empty := &fakeStore{}
	*calls = nil
	if err := p.Send(context.Background(), request(present.EventReminder, nil, empty)); err != nil {
		t.Fatal(err)
	}
	if (*calls)[0].Body["thread_ts"] != nil || empty.saves != 1 {
		t.Fatalf("reminder without a card: %+v saves=%d", (*calls)[0], empty.saves)
	}
}

// "updated" rewrites the card in place, drawn in the alert's current state.
func TestUpdatedRewritesTheCard(t *testing.T) {
	srv, calls := fakeSlack(t, "")
	store := &fakeStore{ref: json.RawMessage(`{"channel":"C0PROBARA","ts":"1.2"}`)}
	req := request(present.EventUpdated, nil, store)
	req.Event.Alert.Status = "acknowledged"
	until := time.Date(2026, 5, 24, 13, 0, 0, 0, time.UTC)
	req.Event.Alert.SnoozedUntil = &until
	if err := newPlugin(srv.URL).Send(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	c := (*calls)[0]
	if c.Method != "chat.update" || c.Body["ts"] != "1.2" || c.Body["channel"] != "C0PROBARA" {
		t.Fatalf("call = %+v", c)
	}
	blocks := blocksJSON(t, c)
	if strings.Contains(blocks, "probara_ack") {
		t.Fatalf("acknowledged card still offers Acknowledge: %s", blocks)
	}
	for _, want := range []string{"Alert Acknowledged", "Snoozed until", "SNOOZED"} {
		if !strings.Contains(blocks, want) {
			t.Fatalf("updated card lacks %q: %s", want, blocks)
		}
	}
	if store.saves != 0 {
		t.Fatal("an update must not save a new reference")
	}

	// No card saved: nothing to rewrite.
	*calls = nil
	if err := newPlugin(srv.URL).Send(context.Background(), request(present.EventUpdated, nil, &fakeStore{})); err != nil || len(*calls) != 0 {
		t.Fatalf("update without a card: err=%v calls=%d", err, len(*calls))
	}
}

type fakeSigner struct{}

func (fakeSigner) ActionToken(_, _, _, _ string) (string, error) { return "tok", nil }

// A channel without interactivity renders the signed acknowledge link on
// the card and on reminders instead of the in-Slack button.
func TestNonInteractiveUsesAckLink(t *testing.T) {
	plugin.Configure(plugin.Runtime{AppBaseURL: "https://probara.example.com", Links: fakeSigner{}})
	defer plugintest.AllowLoopback()
	srv, calls := fakeSlack(t, "")
	cfg := map[string]any{"bot_token": token, "team_id": "T0TEAM", "channel_id": "C0PROBARA", "interactive": false}
	store := &fakeStore{}
	p := newPlugin(srv.URL)
	if err := p.Send(context.Background(), request(present.EventCreated, cfg, store)); err != nil {
		t.Fatal(err)
	}
	if err := p.Send(context.Background(), request(present.EventReminder, cfg, store)); err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		blocks := blocksJSON(t, c)
		if strings.Contains(blocks, "probara_ack") || !strings.Contains(blocks, "https://probara.example.com/alert-action/tok") {
			t.Fatalf("%s: want the ack link, not the button: %s", c.Method, blocks)
		}
	}
}

// A test send posts a card without an Acknowledge button (its alert does
// not exist) and saves nothing.
func TestTestSendHasNoButton(t *testing.T) {
	srv, calls := fakeSlack(t, "")
	req := request(present.EventCreated, nil, nil)
	req.Test = true
	if err := newPlugin(srv.URL).Send(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(blocksJSON(t, (*calls)[0]), "probara_ack") {
		t.Fatal("test card offers Acknowledge")
	}
}

// Slack answers HTTP 200 with ok:false: configuration errors are permanent,
// the rest retried.
func TestAPIErrorClassification(t *testing.T) {
	for code, permanent := range map[string]bool{
		"invalid_auth": true, "channel_not_found": true, "not_in_channel": true,
		"message_not_found": true, "internal_error": false, "fatal_error": false,
	} {
		srv, _ := fakeSlack(t, `{"ok":false,"error":"`+code+`"}`)
		err := newPlugin(srv.URL).Send(context.Background(), request(present.EventCreated, nil, &fakeStore{}))
		if err == nil || !strings.Contains(err.Error(), code) {
			t.Fatalf("%s: err = %v", code, err)
		}
		if plugin.IsPermanent(err) != permanent {
			t.Errorf("%s: permanent = %v, want %v", code, plugin.IsPermanent(err), permanent)
		}
		if strings.Contains(err.Error(), token) {
			t.Fatalf("error leaks the bot token: %v", err)
		}
	}
	plugintest.AssertClassification(t, func(ctx context.Context, serverURL string) error {
		return newPlugin(serverURL).Send(ctx, request(present.EventCreated, nil, &fakeStore{}))
	})
}

func TestMissingConfigIsPermanent(t *testing.T) {
	err := New().Send(context.Background(), request(present.EventCreated, map[string]any{}, nil))
	if !plugin.IsPermanent(err) {
		t.Fatalf("err = %v", err)
	}
}

// Payload golden: the card's block layout for a trigger.
func TestCardGolden(t *testing.T) {
	m := present.BuildWith(plugintest.SampleEvent(), present.EventCreated, present.Options{AppBaseURL: "https://probara.example.com"})
	got, err := json.Marshal(card(m, renderOptions{interactive: true, ackable: true, channelID: "ch-1"}))
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	var blocks []block
	_ = json.Unmarshal(got, &blocks)
	for _, b := range blocks {
		types = append(types, b.Type)
	}
	if strings.Join(types, ",") != "header,section,section,section,actions,context" {
		t.Fatalf("block layout = %v\n%s", types, got)
	}
	for _, want := range []string{
		`"text":":rotating_light: Alert Triggered: API health"`,
		`"action_id":"probara_ack","text":{"type":"plain_text","text":"Acknowledge"},"value":"a-1:ch-1","style":"primary"`,
		`"action_id":"probara_open"`,
		"i/o timeout",
	} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("card lacks %s:\n%s", want, got)
		}
	}
}

func TestEventTypesPluginIgnores(t *testing.T) {
	srv, calls := fakeSlack(t, "")
	if err := newPlugin(srv.URL).Send(context.Background(), request(present.EventAcknowledged, nil, &fakeStore{})); err != nil || len(*calls) != 0 {
		t.Fatalf("acknowledged: err=%v calls=%d", err, len(*calls))
	}
}
