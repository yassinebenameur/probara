// Package bootstrap holds the process wiring every service main repeats: the
// ops endpoints (/healthz, /readyz, /metrics), the wait for a termination
// signal, the at-rest encryptor and the notification runtime.
//
// It exists so the five mains cannot drift apart again. Before it, each main
// carried its own copy and they had diverged: one liveness probe ignored a
// dead NATS connection, readiness probes disagreed on which context bounded
// them. A main still owns what is specific to it (which dependencies it has,
// what it starts, how it shuts down); only the shared mechanics live here.
package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/metrics"
	"github.com/yassinebenameur/probara/shared/netguard"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/email"
	"github.com/yassinebenameur/probara/shared/queue"
	"github.com/yassinebenameur/probara/shared/secrets"
)

const (
	// readyTimeout bounds one /readyz probe across all its checks.
	readyTimeout = 5 * time.Second
	// ShutdownTimeout is how long a service gets to drain after a signal.
	ShutdownTimeout = 30 * time.Second
)

// ReadyCheck is one dependency /readyz probes. Name appears in the response
// body ("Database unavailable") so a failing probe says which one.
type ReadyCheck struct {
	Name  string
	Check func(context.Context) error
}

// DatabaseCheck probes the Postgres connection.
func DatabaseCheck(client *db.Client) ReadyCheck {
	return ReadyCheck{Name: "Database", Check: client.HealthCheck}
}

// QueueCheck probes the NATS connection.
func QueueCheck(client *queue.Client) ReadyCheck {
	return ReadyCheck{Name: "NATS", Check: client.HealthCheck}
}

// Healthz is the liveness probe. It fails only when the NATS connection is
// permanently closed: the client never reconnects from that state, so the
// process can never consume or publish again and must be restarted. A
// reconnecting connection is healthy, and so is a service with no queue
// client at all (pass nil).
func Healthz(queueClient *queue.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if queueClient != nil && queueClient.Closed() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("NATS connection permanently closed"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	}
}

// Readyz is the readiness probe: every check must pass within readyTimeout,
// measured from the probe request so an abandoned probe stops its checks.
func Readyz(log *logger.Logger, checks ...ReadyCheck) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
		defer cancel()

		for _, check := range checks {
			if err := check.Check(ctx); err != nil {
				log.WithError(err).Debugf("%s health check failed", check.Name)
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(check.Name + " unavailable"))
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	}
}

// OpsMux returns a mux serving /healthz, /readyz and /metrics. Callers may
// register more routes on it before serving.
func OpsMux(registry *metrics.Registry, healthz, readyz http.HandlerFunc) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthz)
	mux.HandleFunc("/readyz", readyz)
	mux.Handle("/metrics", registry.Handler())
	return mux
}

// Serve listens on port and blocks; run it in a goroutine. A listener that
// cannot start is fatal: a service without its probes would be killed by the
// orchestrator anyway, with a less useful message.
func Serve(log *logger.Logger, name string, port int, handler http.Handler) {
	server := &http.Server{Addr: fmt.Sprintf(":%d", port), Handler: handler}
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.WithError(err).Fatalf("Failed to start %s server", name)
	}
}

// WaitForShutdownSignal blocks until SIGINT or SIGTERM.
func WaitForShutdownSignal() {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit
}

// ShutdownContext returns the context a service drains under.
func ShutdownContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), ShutdownTimeout)
}

// PlatformEncryptor returns the at-rest encryptor built from
// PROBARA_SECRETS_KEY. With no key configured it returns a no-op encryptor
// and configured=false, which is a supported (plaintext) mode; a key that is
// set but invalid is an error the caller should treat as fatal.
func PlatformEncryptor() (enc secrets.Encryptor, configured bool, err error) {
	keyProvider, err := secrets.NewEnvKeyProvider()
	switch {
	case err == nil:
		return secrets.NewAESGCMEncryptor(keyProvider), true, nil
	case errors.Is(err, secrets.ErrKeyNotConfigured):
		return secrets.NoOpEncryptor{}, false, nil
	default:
		return nil, false, fmt.Errorf("invalid PROBARA_SECRETS_KEY: %w", err)
	}
}

// NotificationOptions tunes ConfigureNotifications for one service.
type NotificationOptions struct {
	// Mailer false skips the SMTP mailer (the egress policy and deep-link
	// origin are still installed). A worker with its notifications consumer
	// off sets it.
	Mailer bool
	// DefaultRecipient is the fallback address for email channels that name
	// none. Only the alerter sets it.
	DefaultRecipient string
	// Purpose names what email is for in this process ("email alerts",
	// "email channel tests"), for the warning when SMTP is missing or broken.
	Purpose string
}

// ConfigureNotifications installs the process-level notification runtime:
// the deep-link origin and egress (SSRF) policy every channel plugin dials
// through, and the SMTP mailer behind the email plugin. The API, the alerter
// and the worker all call it; a binary that does not fails closed (private
// egress blocked, no links, email Send refused).
func ConfigureNotifications(log *logger.Logger, smtp config.SMTPConfig, egress config.NotificationEgressConfig, opts NotificationOptions) {
	plugin.Configure(plugin.Runtime{
		AppBaseURL: smtp.AppBaseURL,
		Egress: netguard.Policy{
			BlockPrivate: egress.NotificationBlockPrivateIPs,
			AllowedCIDRs: egress.NotificationAllowedCIDRs,
		},
	})
	if !opts.Mailer {
		return
	}
	if smtp.SMTPHost == "" || smtp.SMTPFrom == "" {
		log.Warnf("SMTP not configured; %s will fail with mailer not configured", opts.Purpose)
		return
	}
	mailer, err := email.NewSMTPMailer(email.SMTPParams{
		Host:             smtp.SMTPHost,
		Port:             smtp.SMTPPort,
		Username:         smtp.SMTPUsername,
		Password:         smtp.SMTPPassword,
		From:             smtp.SMTPFrom,
		FromName:         smtp.SMTPFromName,
		UseTLS:           smtp.SMTPUseTLS,
		DefaultRecipient: opts.DefaultRecipient,
		AppBaseURL:       smtp.AppBaseURL,
	})
	if err != nil {
		log.WithError(err).Warnf("Failed to configure SMTP mailer; %s will fail until SMTP is fixed", opts.Purpose)
		return
	}
	email.SetMailer(mailer)
}
