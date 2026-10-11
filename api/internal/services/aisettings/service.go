// Package aisettings manages workspace enablement for bundled local AI and
// the workspace's automatic alert triage settings (read by the worker relay).
package aisettings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/yassinebenameur/probara/shared/ai"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
)

type Settings struct {
	Enabled             bool   `json:"enabled"`
	InstallationEnabled bool   `json:"installation_enabled"`
	Available           bool   `json:"available"`
	Model               string `json:"model,omitempty"`
	Message             string `json:"message,omitempty"`
	// Automatic alert triage (ai_settings columns, migration 000094).
	// Values are the row's, or the column defaults without a row. Triage
	// never changes paging.
	AutoTriageEnabled     bool `json:"auto_triage_enabled"`
	TriageDelaySeconds    int  `json:"triage_delay_seconds"`
	TriageCooldownSeconds int  `json:"triage_cooldown_seconds"`
	TriageMaxPerHour      int  `json:"triage_max_per_hour"`
	// TriageNotificationsEnabled opts the workspace into the alerter's
	// suspected-cause follow-ups (migration 000095): one advisory message per
	// already-paged channel. Effective only with Enabled and
	// AutoTriageEnabled; turning it on stamps triage_notifications_enabled_at
	// so only triages completed afterwards are sent.
	TriageNotificationsEnabled bool `json:"triage_notifications_enabled"`
}

// UpdateRequest is the PUT body: every field optional, at least one present.
// An absent field keeps the stored value (or the default on first write).
type UpdateRequest struct {
	Enabled               *bool `json:"enabled,omitempty"`
	AutoTriageEnabled     *bool `json:"auto_triage_enabled,omitempty"`
	TriageDelaySeconds    *int  `json:"triage_delay_seconds,omitempty"`
	TriageCooldownSeconds *int  `json:"triage_cooldown_seconds,omitempty"`
	TriageMaxPerHour      *int  `json:"triage_max_per_hour,omitempty"`

	TriageNotificationsEnabled *bool `json:"triage_notifications_enabled,omitempty"`
}

// Triage setting defaults and ranges. They mirror the ai_settings column
// defaults and CHECK constraints of migration 000094 and the web form's
// validation; change all three together.
const (
	DefaultTriageDelaySeconds    = 60
	DefaultTriageCooldownSeconds = 900
	DefaultTriageMaxPerHour      = 60

	MaxTriageDelaySeconds    = 3600
	MaxTriageCooldownSeconds = 86400
	MinTriageMaxPerHour      = 1
	MaxTriageMaxPerHour      = 600
)

// ValidationError is a rejected update (400 validation_error).
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

// Validate checks the update against the pinned ranges.
func (r UpdateRequest) Validate() error {
	if r.Enabled == nil && r.AutoTriageEnabled == nil && r.TriageDelaySeconds == nil &&
		r.TriageCooldownSeconds == nil && r.TriageMaxPerHour == nil && r.TriageNotificationsEnabled == nil {
		return &ValidationError{Msg: "at least one setting is required"}
	}
	if v := r.TriageDelaySeconds; v != nil && (*v < 0 || *v > MaxTriageDelaySeconds) {
		return &ValidationError{Msg: "triage_delay_seconds must be between 0 and 3600"}
	}
	if v := r.TriageCooldownSeconds; v != nil && (*v < 0 || *v > MaxTriageCooldownSeconds) {
		return &ValidationError{Msg: "triage_cooldown_seconds must be between 0 and 86400"}
	}
	if v := r.TriageMaxPerHour; v != nil && (*v < MinTriageMaxPerHour || *v > MaxTriageMaxPerHour) {
		return &ValidationError{Msg: "triage_max_per_hour must be between 1 and 600"}
	}
	return nil
}

type TestRequest struct{}
type TestResult struct {
	OK      bool   `json:"ok"`
	Model   string `json:"model,omitempty"`
	Message string `json:"message,omitempty"`
}

// User-facing readiness messages. Probe errors wrap *url.Error, which names
// the internal LOCAL_AI_URL dial address, and POST /ai-settings/test is
// viewer-callable (postReadPaths), so only these fixed strings ever reach a
// response; the detailed error is logged server-side.
const (
	MessageDisabled      = "Local AI is disabled for this installation."
	MessageMisconfigured = "Local AI is not configured correctly for this installation."
	MessageUnreachable   = "Local AI service is unreachable."
	MessageNotReady      = "Local AI service is not ready."
)

const (
	// readinessTTL bounds how stale GET /ai-settings may report the service.
	// The incident page loads settings on every visit, so without the cache
	// each page view paid a synchronous probe (up to probeTimeout when down).
	readinessTTL = 30 * time.Second
	probeTimeout = 3 * time.Second
)

type Service struct {
	db     *shareddb.Client
	config ai.Config
	log    logrus.FieldLogger

	// probe and now are seams for tests; production uses ai.Probe/time.Now.
	probe func(context.Context, ai.Config) (string, error)
	now   func() time.Time

	// Readiness cache, process-local. inflight is the probe GETs join
	// (single-flight); seq orders probes so a slower, older probe finishing
	// after a newer one (e.g. a forced Test) cannot overwrite its result.
	//
	// Forced probes are coalesced too: at most one runs (forcing) and at most
	// one waits behind it (forceNext), which every Test arriving during the
	// running one joins. Probes run detached from the request and Test is
	// viewer-callable, so without this a client firing and aborting Tests
	// would pile up one outbound probe per request.
	mu        sync.Mutex
	cached    TestResult
	cachedSeq uint64
	expires   time.Time
	inflight  *probeCall
	forcing   *probeCall
	forceNext *probeCall
	seq       uint64
}

type probeCall struct {
	seq    uint64 // assigned when the probe starts
	ctx    context.Context
	done   chan struct{}
	result TestResult
}

func NewService(db *shareddb.Client, config ai.Config) *Service {
	return &Service{
		db:     db,
		config: config,
		log:    logrus.StandardLogger(),
		probe:  ai.Probe,
		now:    time.Now,
	}
}

// SetLogger routes the detailed (unsanitized) probe errors to the service
// logger. Call it during wiring, before the service handles requests.
func (s *Service) SetLogger(l *logger.Logger) {
	if l != nil {
		s.log = l.WithField("component", "aisettings")
	}
}

func (s *Service) EffectiveEnabled(ctx context.Context, id uuid.UUID) (bool, error) {
	return ai.EffectiveEnabled(ctx, s.db, id, s.config.Disabled)
}
func (s *Service) EffectiveConfig(ctx context.Context, id uuid.UUID) (ai.Config, bool, error) {
	enabled, err := s.EffectiveEnabled(ctx, id)
	return s.config, enabled, err
}
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Settings, error) {
	settings, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	if s.config.Disabled {
		settings.Message = MessageDisabled
		return settings, nil
	}
	probe := s.readiness(ctx, false)
	settings.Available = probe.OK
	settings.Model = probe.Model
	settings.Message = probe.Message
	return settings, nil
}

// load reads the workspace row, falling back to the column defaults. Enabled
// follows ai.EffectiveEnabled: installation disabling wins, then the row,
// then true.
func (s *Service) load(ctx context.Context, id uuid.UUID) (*Settings, error) {
	settings := &Settings{
		Enabled:               true,
		InstallationEnabled:   !s.config.Disabled,
		TriageDelaySeconds:    DefaultTriageDelaySeconds,
		TriageCooldownSeconds: DefaultTriageCooldownSeconds,
		TriageMaxPerHour:      DefaultTriageMaxPerHour,
	}
	err := s.db.QueryRowContext(ctx, `
		SELECT enabled, auto_triage_enabled, triage_delay_seconds, triage_cooldown_seconds, triage_max_per_hour,
			triage_notifications_enabled
		FROM ai_settings WHERE tenant_id = $1`, id).Scan(&settings.Enabled, &settings.AutoTriageEnabled,
		&settings.TriageDelaySeconds, &settings.TriageCooldownSeconds, &settings.TriageMaxPerHour,
		&settings.TriageNotificationsEnabled)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("load workspace AI settings: %w", err)
	}
	if s.config.Disabled {
		settings.Enabled = false
	}
	return settings, nil
}

// Update applies a partial update. Range errors are *ValidationError;
// anything else is a server error. Turning auto-triage (or suspected-cause
// notifications) on while a switch it depends on is off is accepted: the
// relay and the alerter only act for workspaces with every switch on.
// triage_notifications_enabled_at is stamped when the flag turns on and
// cleared when it turns off; re-saving it while on keeps the stamp.
func (s *Service) Update(ctx context.Context, id uuid.UUID, req UpdateRequest) (*Settings, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO ai_settings (tenant_id, enabled, auto_triage_enabled, triage_delay_seconds, triage_cooldown_seconds, triage_max_per_hour,
			triage_notifications_enabled, triage_notifications_enabled_at)
		VALUES ($1, COALESCE($2::boolean, TRUE), COALESCE($3::boolean, FALSE), COALESCE($4::int, 60), COALESCE($5::int, 900), COALESCE($6::int, 60),
			COALESCE($7::boolean, FALSE), CASE WHEN $7::boolean THEN NOW() END)
		ON CONFLICT (tenant_id) DO UPDATE SET
			enabled = COALESCE($2::boolean, ai_settings.enabled),
			auto_triage_enabled = COALESCE($3::boolean, ai_settings.auto_triage_enabled),
			triage_delay_seconds = COALESCE($4::int, ai_settings.triage_delay_seconds),
			triage_cooldown_seconds = COALESCE($5::int, ai_settings.triage_cooldown_seconds),
			triage_max_per_hour = COALESCE($6::int, ai_settings.triage_max_per_hour),
			triage_notifications_enabled = COALESCE($7::boolean, ai_settings.triage_notifications_enabled),
			triage_notifications_enabled_at = CASE
				WHEN $7::boolean IS NULL THEN ai_settings.triage_notifications_enabled_at
				WHEN NOT $7::boolean THEN NULL
				WHEN ai_settings.triage_notifications_enabled THEN ai_settings.triage_notifications_enabled_at
				ELSE NOW()
			END,
			updated_at = NOW()`,
		id, req.Enabled, req.AutoTriageEnabled, req.TriageDelaySeconds, req.TriageCooldownSeconds, req.TriageMaxPerHour,
		req.TriageNotificationsEnabled)
	if err != nil {
		return nil, fmt.Errorf("update workspace AI settings: %w", err)
	}
	return s.Get(ctx, id)
}

// Test probes the installation's service regardless of the workspace enable
// switch. It always runs a fresh probe (bypassing the readiness cache) and
// stores the result, so an operator who just fixed the service sees it on the
// next GET too.
func (s *Service) Test(ctx context.Context, _ uuid.UUID, _ TestRequest) TestResult {
	if s.config.Disabled {
		return TestResult{Message: MessageDisabled}
	}
	return s.readiness(ctx, true)
}

// readiness returns the cached probe result while fresh; otherwise it joins
// the in-flight probe or starts one. force skips the cache and never joins a
// probe that was already running when the caller arrived (it may predate the
// caller's fix): it starts a probe, or joins the single one queued to start
// when the running forced probe finishes.
//
// The probe runs on its own goroutine with a context detached from the
// caller, so one cancelled request cannot fail the probe other requests are
// waiting on; the caller still stops waiting when its own context ends.
func (s *Service) readiness(ctx context.Context, force bool) TestResult {
	s.mu.Lock()
	if !force && s.cachedSeq > 0 && s.now().Before(s.expires) {
		r := s.cached
		s.mu.Unlock()
		return r
	}
	var call *probeCall
	switch {
	case !force && s.inflight != nil:
		call = s.inflight
	case force && s.forcing != nil:
		if s.forceNext == nil {
			s.forceNext = newProbeCall(ctx)
		}
		call = s.forceNext
	default:
		call = newProbeCall(ctx)
		s.startLocked(call, force)
	}
	s.mu.Unlock()

	select {
	case <-call.done:
		return call.result
	case <-ctx.Done():
		return TestResult{Message: MessageUnreachable}
	}
}

func newProbeCall(ctx context.Context) *probeCall {
	return &probeCall{ctx: context.WithoutCancel(ctx), done: make(chan struct{})}
}

// startLocked launches call; s.mu must be held.
func (s *Service) startLocked(call *probeCall, force bool) {
	s.seq++
	call.seq = s.seq
	s.inflight = call
	if force {
		s.forcing = call
	}
	go s.runProbe(call.ctx, call)
}

func (s *Service) runProbe(ctx context.Context, call *probeCall) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	model, err := s.probe(ctx, s.config)
	result := TestResult{OK: true, Model: model}
	if err != nil {
		result = TestResult{Message: s.classify(err)}
		s.log.WithError(err).Warn("Local AI readiness probe failed")
	}

	s.mu.Lock()
	call.result = result
	if call.seq > s.cachedSeq {
		s.cached, s.cachedSeq, s.expires = result, call.seq, s.now().Add(readinessTTL)
	}
	if s.inflight == call {
		s.inflight = nil
	}
	if s.forcing == call {
		s.forcing = nil
		if next := s.forceNext; next != nil {
			s.forceNext = nil
			s.startLocked(next, true)
		}
	}
	s.mu.Unlock()
	close(call.done)
}

// classify maps a probe error to one of the fixed user-facing messages.
func (s *Service) classify(err error) string {
	if errors.Is(err, ai.ErrNotConfigured) {
		return MessageDisabled
	}
	// Probe builds its client through NewAnalyzer, so a config it rejects
	// (bad LOCAL_AI_URL, short LOCAL_AI_TOKEN) fails here the same way.
	if _, cfgErr := ai.NewAnalyzer(s.config); cfgErr != nil {
		return MessageMisconfigured
	}
	var urlErr *url.Error
	var netErr net.Error
	if errors.As(err, &urlErr) || errors.As(err, &netErr) ||
		errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return MessageUnreachable
	}
	return MessageNotReady
}
