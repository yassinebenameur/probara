package email

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"net/textproto"
	"time"

	"github.com/yassinebenameur/probara/shared/mail"
)

// smtpMessageTimeout bounds one message's SMTP I/O (and the connect,
// greeting, STARTTLS and AUTH before the first one) when the caller's ctx
// carries no earlier deadline. Without it a relay that accepts the
// connection and then goes silent would hold the sender forever. A var so
// tests can shorten it.
var smtpMessageTimeout = 30 * time.Second

// smtpSession is one authenticated SMTP connection that carries any number
// of MAIL/RCPT/DATA transactions. Every I/O step runs under a connection
// deadline (deadlineFor), and cancelling the ctx it was opened with
// interrupts whatever I/O is in flight.
type smtpSession struct {
	conn       net.Conn
	client     *smtp.Client
	stopCancel func() bool
}

// deadlineFor is the I/O deadline for the next step: the ctx deadline, or
// smtpMessageTimeout from now when that comes first. A ctx already cancelled
// (with or without a deadline) gets now: the cancel interrupt pulled the
// deadline in once, and every step that sets a fresh one must not push it
// back out.
func deadlineFor(ctx context.Context) time.Time {
	if ctx.Err() != nil {
		return time.Now()
	}
	deadline := time.Now().Add(smtpMessageTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		return d
	}
	return deadline
}

// openSMTPSession connects, reads the greeting and authenticates, keeping
// what each mode did before: implicit TLS (UseTLS) authenticates only when
// the server offers AUTH; plain mode upgrades with STARTTLS when offered and
// fails when credentials are set but AUTH is not offered, as smtp.SendMail
// does.
func (m *SMTPMailer) openSMTPSession(ctx context.Context) (*smtpSession, error) {
	addr := net.JoinHostPort(m.cfg.Host, fmt.Sprint(m.cfg.Port))
	deadline := deadlineFor(ctx)
	dialCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	var conn net.Conn
	var err error
	if m.cfg.UseTLS {
		dialer := &tls.Dialer{Config: &tls.Config{ServerName: m.cfg.Host}}
		conn, err = dialer.DialContext(dialCtx, "tcp", addr)
	} else {
		var dialer net.Dialer
		conn, err = dialer.DialContext(dialCtx, "tcp", addr)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to dial smtp server: %w", err)
	}
	// Cancelling ctx pulls the deadline in, which unblocks any read or
	// write in progress.
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	fail := func(err error) (*smtpSession, error) {
		stop()
		_ = conn.Close()
		return nil, err
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return fail(err)
	}

	client, err := smtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		return fail(fmt.Errorf("failed to create smtp client: %w", err))
	}
	var auth smtp.Auth
	if m.cfg.Username != "" {
		auth = smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)
	}
	if m.cfg.UseTLS {
		if auth != nil {
			if ok, _ := client.Extension("AUTH"); ok {
				if err := client.Auth(auth); err != nil {
					return fail(fmt.Errorf("smtp auth failed: %w", err))
				}
			}
		}
	} else {
		if err := client.Hello("localhost"); err != nil {
			return fail(fmt.Errorf("smtp hello failed: %w", err))
		}
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{ServerName: m.cfg.Host}); err != nil {
				return fail(fmt.Errorf("smtp starttls failed: %w", err))
			}
		}
		if auth != nil {
			if ok, _ := client.Extension("AUTH"); !ok {
				return fail(errors.New("smtp: server doesn't support AUTH"))
			}
			if err := client.Auth(auth); err != nil {
				return fail(fmt.Errorf("smtp auth failed: %w", err))
			}
		}
	}
	return &smtpSession{conn: conn, client: client, stopCancel: stop}, nil
}

// errStage says how far a failed transaction got. Before the end-of-data
// marker the server cannot have accepted the message, so a retry on a fresh
// connection cannot duplicate it.
type errStage int

const (
	stageBeforeData errStage = iota // MAIL, RCPT, DATA command or body write
	stageAfterData                  // the reply to the final "." — may be delivered
)

// send runs one MAIL/RCPT/DATA transaction under a fresh deadline.
func (s *smtpSession) send(ctx context.Context, from string, to []string, message string) (errStage, error) {
	if err := s.conn.SetDeadline(deadlineFor(ctx)); err != nil {
		return stageBeforeData, err
	}
	if err := s.client.Mail(from); err != nil {
		return stageBeforeData, fmt.Errorf("smtp from failed: %w", err)
	}
	for _, recipient := range to {
		if err := s.client.Rcpt(recipient); err != nil {
			return stageBeforeData, fmt.Errorf("smtp rcpt failed: %w", err)
		}
	}
	writer, err := s.client.Data()
	if err != nil {
		return stageBeforeData, fmt.Errorf("smtp data failed: %w", err)
	}
	if _, err := writer.Write([]byte(message)); err != nil {
		return stageBeforeData, fmt.Errorf("smtp write failed: %w", err)
	}
	if err := writer.Close(); err != nil {
		// A reply means the relay refused the message; no reply at all
		// leaves its fate unknown.
		var reply *textproto.Error
		if !errors.As(err, &reply) {
			err = fmt.Errorf("%w: %w", mail.ErrMaybeDelivered, err)
		}
		return stageAfterData, fmt.Errorf("smtp close failed: %w", err)
	}
	return stageBeforeData, nil
}

// reset clears a transaction the server refused, so the connection can carry
// the next one.
func (s *smtpSession) reset(ctx context.Context) error {
	if err := s.conn.SetDeadline(deadlineFor(ctx)); err != nil {
		return err
	}
	return s.client.Reset()
}

// quit ends the session politely and releases the connection.
func (s *smtpSession) quit(ctx context.Context) error {
	defer s.close()
	if err := s.conn.SetDeadline(deadlineFor(ctx)); err != nil {
		return err
	}
	if err := s.client.Quit(); err != nil {
		return fmt.Errorf("smtp quit failed: %w", err)
	}
	return nil
}

// close drops the connection without QUIT.
func (s *smtpSession) close() {
	s.stopCancel()
	_ = s.client.Close()
}

// connectionLost reports whether err leaves the session unusable: an I/O
// failure (including a deadline), or a 421 reply, the server announcing it
// is closing the channel. Any other SMTP reply is a refusal of this one
// transaction on a connection that still works.
func connectionLost(err error) bool {
	var protoErr *textproto.Error
	if errors.As(err, &protoErr) {
		return protoErr.Code == 421
	}
	return true
}
