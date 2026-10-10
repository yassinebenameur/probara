package statuspage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/webpush"
)

const (
	// pushLoopInterval is how often the reconciler and sender run. NATS wakes
	// the loop earlier when it can (see Subscriber), but correctness never
	// depends on that -- this tick alone is sufficient.
	pushLoopInterval = 15 * time.Second

	// pushClaimBatch bounds one pass so a large backlog is drained over
	// several ticks rather than in one long transaction.
	pushClaimBatch = 50

	// pushClaimStaleAfter is when a 'sending' row may be reclaimed, i.e. the
	// claiming replica is presumed dead.
	pushClaimStaleAfter = 5 * time.Minute

	// pushSendConcurrency bounds simultaneous outbound requests. A page with
	// thousands of subscribers must not open thousands of sockets.
	pushSendConcurrency = 16

	// pushSendTimeout bounds one request to a push service.
	pushSendTimeout = 10 * time.Second

	// pushTTLSeconds is the RFC 8030 TTL: how long the push service should
	// hold the message for an offline device. Four hours is long enough to
	// reach someone who closed their laptop, short enough that an outage
	// notification cannot arrive a day late and out of context.
	pushTTLSeconds = 4 * 60 * 60

	// Retention for the delivery ledger. It only needs to outlive the
	// reconciler's lookback window for dedup to hold.
	pushDeliveryRetention = 7 * 24 * time.Hour

	// Subscription hygiene backstops. The authoritative cleanup is a 404/410
	// at send time; these catch endpoints that fail some other way forever,
	// and browsers that simply stopped visiting.
	pushFailureCap = 20
	pushStaleAfter = 180 * 24 * time.Hour
	pushPruneEvery = time.Hour
)

// pushPayload is what the service worker receives, after decryption. Kept
// small on purpose: RFC 8291 gives roughly 3.9 KB of plaintext per message,
// and some push services are stricter.
type pushPayload struct {
	Version int    `json:"v"`
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	Tag     string `json:"tag"`
	TS      int64  `json:"ts"`
	URL     string `json:"url,omitempty"`
}

// pushSender turns claimed delivery rows into encrypted Web Push requests.
type pushSender struct {
	store  *pushStore
	cfg    *config.StatusPageConfig
	logger *logger.Logger
	client *http.Client

	stop     chan struct{}
	stopped  chan struct{}
	wake     chan struct{}
	stopOnce sync.Once
}

func newPushSender(store *pushStore, cfg *config.StatusPageConfig, log *logger.Logger) *pushSender {
	return &pushSender{
		store:   store,
		cfg:     cfg,
		logger:  log,
		client:  &http.Client{Timeout: pushSendTimeout},
		stop:    make(chan struct{}),
		stopped: make(chan struct{}),
		// Buffered by one: a wake while a pass is already running is
		// collapsed into the next pass rather than queuing.
		wake: make(chan struct{}, 1),
	}
}

// Start runs the reconcile-and-send loop until Stop.
func (p *pushSender) Start() {
	go p.run()
}

// Wake asks for an earlier pass. Used by the NATS subscriber as a latency
// hint only -- dropping every wake would delay notifications to the next
// tick, never lose them.
func (p *pushSender) Wake() {
	if p == nil {
		return
	}
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func (p *pushSender) Stop() {
	if p == nil {
		return
	}
	p.stopOnce.Do(func() { close(p.stop) })
	<-p.stopped
}

func (p *pushSender) run() {
	defer close(p.stopped)

	ticker := time.NewTicker(pushLoopInterval)
	defer ticker.Stop()
	pruneTicker := time.NewTicker(pushPruneEvery)
	defer pruneTicker.Stop()

	for {
		select {
		case <-p.stop:
			return
		case <-ticker.C:
			p.pass()
		case <-p.wake:
			p.pass()
		case <-pruneTicker.C:
			p.prune()
		}
	}
}

// pass reconciles the timeline into delivery rows, then sends whatever this
// replica manages to claim.
func (p *pushSender) pass() {
	ctx, cancel := context.WithTimeout(context.Background(), pushLoopInterval*4)
	defer cancel()

	if _, err := p.store.reconcileNotifications(ctx); err != nil {
		p.logger.WithError(err).Warn("Failed to reconcile status page push notifications")
		// Still try to send: a reconcile failure must not strand rows that
		// are already pending.
	}

	claimed, err := p.store.ClaimDeliveries(ctx, pushClaimBatch, pushClaimStaleAfter)
	if err != nil {
		p.logger.WithError(err).Warn("Failed to claim status page push notifications")
		return
	}
	for _, delivery := range claimed {
		p.deliver(ctx, delivery)
	}
}

func (p *pushSender) prune() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	if _, err := p.store.pruneDeliveries(ctx, pushDeliveryRetention); err != nil {
		p.logger.WithError(err).Warn("Failed to prune status page push deliveries")
	}
	if n, err := p.store.PruneSubscriptions(ctx, pushFailureCap, pushStaleAfter); err != nil {
		p.logger.WithError(err).Warn("Failed to prune status page push subscriptions")
	} else if n > 0 {
		p.logger.WithFields(map[string]interface{}{"removed": n}).
			Info("Pruned dead status page push subscriptions")
	}
}

// deliver fans one notification out to every browser subscribed to its page.
func (p *pushSender) deliver(ctx context.Context, d pendingDelivery) {
	subs, err := p.store.SubscriptionsForPage(ctx, d.PageID)
	if err != nil {
		p.logger.WithError(err).Warn("Failed to load subscriptions for push delivery")
		return
	}

	body, err := json.Marshal(p.payloadFor(d))
	if err != nil {
		p.logger.WithError(err).Error("Failed to encode push payload")
		_ = p.store.CompleteDelivery(ctx, d.ID, 0, len(subs))
		return
	}

	var mu sync.Mutex
	var sent, failed int

	group, gctx := errgroup.WithContext(ctx)
	group.SetLimit(pushSendConcurrency)
	for _, sub := range subs {
		group.Go(func() error {
			ok := p.send(gctx, sub, body)
			mu.Lock()
			if ok {
				sent++
			} else {
				failed++
			}
			mu.Unlock()
			// Never return an error: one dead endpoint must not cancel the
			// fan-out for everyone else on the page.
			return nil
		})
	}
	_ = group.Wait()

	if err := p.store.CompleteDelivery(ctx, d.ID, sent, failed); err != nil {
		p.logger.WithError(err).Warn("Failed to record push delivery outcome")
	}
}

// pushOutcome is how one delivery attempt ended. Separating classification
// from the store side effects keeps the security-critical rule -- which
// responses may delete a subscription -- testable without a database.
type pushOutcome int

const (
	// pushOutcomeSent: the push service accepted the message.
	pushOutcomeSent pushOutcome = iota
	// pushOutcomeGone: the subscription no longer exists and MUST be deleted.
	// Only 404 and 410 produce this.
	pushOutcomeGone
	// pushOutcomeFailed: a transient or configuration failure. The
	// subscription stays; its failure count grows.
	pushOutcomeFailed
	// pushOutcomeConfigError: our own credentials or payload are wrong. The
	// subscription is blameless, so its failure count is left alone --
	// otherwise a misconfigured deployment would retire every subscription
	// it has over a few hours.
	pushOutcomeConfigError
)

// send encrypts and posts one notification, then applies the store side
// effects implied by the outcome.
func (p *pushSender) send(ctx context.Context, sub pushSubscriptionRow, payload []byte) bool {
	switch outcome := p.sendClassify(ctx, sub, payload); outcome {
	case pushOutcomeSent:
		p.recordResult(ctx, sub.ID, true)
		return true
	case pushOutcomeGone:
		// RFC 8030: the subscription is gone -- browser uninstalled, site
		// data cleared, or the endpoint expired. This is the only
		// authoritative delete signal.
		if err := p.store.DeleteSubscription(ctx, sub.ID); err != nil {
			p.logger.WithError(err).Warn("Failed to delete expired push subscription")
		}
		return false
	case pushOutcomeConfigError:
		return false
	default:
		p.recordResult(ctx, sub.ID, false)
		return false
	}
}

// sendClassify performs one delivery attempt and reports how it ended,
// without touching the database.
func (p *pushSender) sendClassify(ctx context.Context, sub pushSubscriptionRow, payload []byte) pushOutcome {
	sealed, err := webpush.Encrypt(webpush.Subscription{
		Endpoint: sub.Endpoint,
		P256dh:   sub.P256dh,
		Auth:     sub.Auth,
	}, payload)
	if err != nil {
		// The keys were validated at subscribe time, so this is a stored row
		// that can never be encrypted for: count it against the subscription
		// and let the failure cap retire it.
		return pushOutcomeFailed
	}

	auth, err := webpush.AuthorizationHeader(sub.Endpoint, p.cfg.VAPIDSubject, webpush.Keys{
		Public:  p.cfg.VAPIDPublicKey,
		Private: p.cfg.VAPIDPrivateKey,
	})
	if err != nil {
		// Misconfiguration, not a bad subscription -- see the 403 note below.
		p.logger.WithError(err).Error("Failed to build VAPID authorization header")
		return pushOutcomeConfigError
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(sealed))
	if err != nil {
		return pushOutcomeConfigError
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("TTL", strconv.Itoa(pushTTLSeconds))
	req.Header.Set("Urgency", "high")
	req.Header.Set("Authorization", auth)

	resp, err := p.client.Do(req)
	if err != nil {
		return pushOutcomeFailed
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return pushOutcomeSent

	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return pushOutcomeGone

	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized:
		// NOT gone. A push service answers 403 when the VAPID signature does
		// not match the key the subscription was created with, so treating it
		// as death would delete every subscription on the platform the moment
		// a key is rotated or misconfigured. The subscription is blameless,
		// so its failure count is left alone too.
		p.logger.WithFields(map[string]interface{}{"status": resp.StatusCode}).
			Error("Push service rejected the VAPID credentials; check STATUS_PAGE_VAPID_* configuration")
		return pushOutcomeConfigError

	case resp.StatusCode == http.StatusRequestEntityTooLarge:
		// Our payload, not their subscription.
		p.logger.Error("Push service rejected the payload as too large")
		return pushOutcomeConfigError

	default:
		return pushOutcomeFailed
	}
}

func (p *pushSender) recordResult(ctx context.Context, id uuid.UUID, ok bool) {
	if err := p.store.RecordSendResult(ctx, id, ok); err != nil {
		p.logger.WithError(err).Debug("Failed to record push send result")
	}
}

// payloadFor builds the notification a visitor sees.
func (p *pushSender) payloadFor(d pendingDelivery) pushPayload {
	name := strings.TrimSpace(d.MonitorName)
	if name == "" {
		name = "A service"
	}

	body := name + " is down"
	if d.Kind == string(pushKindRecovered) {
		body = name + " is back up"
	}

	title := strings.TrimSpace(d.PageTitle)
	if title == "" {
		title = "Status update"
	}

	return pushPayload{
		Version: 1,
		Kind:    d.Kind,
		Title:   title,
		Body:    body,
		// One slot per monitor, so a component's recovery replaces its outage
		// rather than stacking a second banner.
		Tag: "m:" + d.MonitorID.String(),
		TS:  time.Now().UTC().UnixMilli(),
		URL: p.pageURL(d.Slug),
	}
}

// pageURL builds the deep link. Empty when STATUS_PAGE_BASE_URL is unset, in
// which case the service worker falls back to its own registration scope,
// which is the page URL by construction -- never a guessed host.
func (p *pushSender) pageURL(slug string) string {
	base := strings.TrimRight(strings.TrimSpace(p.cfg.StatusPageBaseURL), "/")
	if base == "" {
		return ""
	}
	return fmt.Sprintf("%s/public/status/%s", base, slug)
}
