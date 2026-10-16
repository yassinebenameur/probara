package worker

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yassinebenameur/probara/shared/models"
)

// sipWriteTimeout bounds one write on a flow, so a peer that stops reading
// a stream cannot hold a check past its budget.
const sipWriteTimeout = 5 * time.Second

// sipInbound is one message read off a flow, stamped on arrival.
type sipInbound struct {
	msg *sipMessage
	at  time.Time
}

// sipFlowTiming records how long transport setup took.
type sipFlowTiming struct {
	Connect      time.Duration // UDP: socket setup; TCP/TLS: TCP connect
	TLSHandshake time.Duration
}

// sipFlow is one SIP transport connection: a connected UDP socket, a TCP
// connection or a TLS connection, always to the single peer dialed through
// dialGuard. Every request and every response, in-dialog traffic included,
// travels over it: SIP signalling never dials an address learned from the
// peer (Contact, Record-Route, Via received/rport, SDP), so the SSRF policy
// checked at dial time covers the whole exchange. A connected UDP socket also
// only accepts datagrams from that peer.
//
// One reader goroutine frames inbound messages onto inbound, which it closes
// when the connection fails or is closed; Err then says why.
type sipFlow struct {
	transport string // udp, tcp or tls
	conn      net.Conn
	localIP   net.IP
	localPort int
	peer      net.Addr
	tlsState  *tls.ConnectionState
	timing    sipFlowTiming

	inbound   chan sipInbound
	malformed atomic.Int64 // datagrams dropped as unparseable

	writeMu   sync.Mutex
	closed    bool
	closing   chan struct{}
	closeOnce sync.Once
	err       error // set before inbound is closed
}

// dialSIP opens a flow to host:port. The TLS identity is tls_server_name,
// else the configured host.
func (c *SIPChecker) dialSIP(ctx context.Context, transport, host string, port int, config *models.SIPMonitorConfig) (*sipFlow, error) {
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	timeout := 30 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
	}
	guard := newDialGuard(c.blockPrivateIPs, c.allowedCIDRs, timeout)

	network := "tcp"
	if transport == "udp" {
		network = "udp"
	}
	start := time.Now()
	conn, err := guard.DialContext(ctx, network, addr)
	if err != nil {
		return nil, &sipDialError{err: err}
	}
	timing := sipFlowTiming{Connect: time.Since(start)}

	var tlsState *tls.ConnectionState
	if transport == "tls" {
		serverName := strings.TrimSpace(config.TLSServerName)
		if serverName == "" {
			serverName = config.Host
		}
		tlsConn := tls.Client(conn, &tls.Config{
			ServerName:         serverName,
			InsecureSkipVerify: config.TLSSkipVerify, //nolint:gosec // explicit user opt-in for lab/self-signed servers
			MinVersion:         tls.VersionTLS12,
		})
		handshakeStart := time.Now()
		if deadline, ok := ctx.Deadline(); ok {
			err = tlsConn.SetDeadline(deadline)
		}
		if err == nil {
			err = tlsConn.HandshakeContext(ctx)
		}
		if err == nil {
			// The reader runs without deadlines; Close unblocks it.
			err = tlsConn.SetDeadline(time.Time{})
		}
		if err != nil {
			conn.Close()
			// The TCP connection is up: this is a TLS failure (bad,
			// expired or untrusted certificate, protocol mismatch),
			// reported as such rather than as a connect error.
			return nil, &sipDialError{handshake: true, err: err}
		}
		timing.TLSHandshake = time.Since(handshakeStart)
		state := tlsConn.ConnectionState()
		tlsState = &state
		conn = tlsConn
	}

	flow := newSIPFlow(transport, conn)
	flow.tlsState = tlsState
	flow.timing = timing
	flow.start()
	return flow, nil
}

// newSIPFlow wraps an established connection. The caller calls start.
func newSIPFlow(transport string, conn net.Conn) *sipFlow {
	f := &sipFlow{
		transport: transport,
		conn:      conn,
		peer:      conn.RemoteAddr(),
		inbound:   make(chan sipInbound, 32),
		closing:   make(chan struct{}),
	}
	if host, port, err := net.SplitHostPort(conn.LocalAddr().String()); err == nil {
		f.localIP = net.ParseIP(host)
		f.localPort, _ = strconv.Atoi(port)
	}
	return f
}

// reliable reports a stream transport: no SIP-level retransmission there
// (RFC 3261 §17.1.1.2, §17.1.2.2).
func (f *sipFlow) reliable() bool { return f.transport != "udp" }

func (f *sipFlow) start() { go f.readLoop() }

func (f *sipFlow) readLoop() {
	var err error
	defer func() {
		f.err = err
		close(f.inbound)
	}()

	if f.transport == "udp" {
		buffer := make([]byte, 65535)
		for {
			n, readErr := f.conn.Read(buffer)
			if readErr != nil {
				err = readErr
				return
			}
			at := time.Now()
			msg, parseErr := parseSIPMessage(buffer[:n])
			if parseErr != nil {
				// A malformed datagram is discarded (RFC 3261 §18.3);
				// the transaction keeps waiting.
				f.malformed.Add(1)
				continue
			}
			if !f.deliver(sipInbound{msg: msg, at: at}) {
				err = net.ErrClosed
				return
			}
		}
	}

	reader := bufio.NewReaderSize(f.conn, sipMaxHeadBytes+1024)
	for {
		msg, readErr := readSIPStreamMessage(reader)
		if readErr != nil {
			// On a stream a framing error loses message boundaries for
			// good, so it ends the flow like a read error does.
			err = readErr
			return
		}
		if !f.deliver(sipInbound{msg: msg, at: time.Now()}) {
			err = net.ErrClosed
			return
		}
	}
}

func (f *sipFlow) deliver(in sipInbound) bool {
	select {
	case f.inbound <- in:
		return true
	case <-f.closing:
		return false
	}
}

// Err is why the flow stopped reading. Only valid once inbound is closed.
func (f *sipFlow) Err() error {
	if f.err == nil {
		return net.ErrClosed
	}
	return f.err
}

// send writes one message. Writes are serialized; after Close it returns
// net.ErrClosed.
func (f *sipFlow) send(b []byte) error {
	f.writeMu.Lock()
	defer f.writeMu.Unlock()
	if f.closed {
		return net.ErrClosed
	}
	if err := f.conn.SetWriteDeadline(time.Now().Add(sipWriteTimeout)); err != nil {
		return err
	}
	_, err := f.conn.Write(b)
	return err
}

// Close closes the connection and stops the reader.
func (f *sipFlow) Close() {
	f.closeOnce.Do(func() {
		f.writeMu.Lock()
		f.closed = true
		f.writeMu.Unlock()
		close(f.closing)
		f.conn.Close()
	})
}

// sentBy is the local address as host:port, IPv6 in brackets.
func (f *sipFlow) sentBy() string {
	host := "0.0.0.0"
	if f.localIP != nil {
		host = f.localIP.String()
	}
	return net.JoinHostPort(host, strconv.Itoa(f.localPort))
}

// viaValue is the top Via for a request on this flow: the real local
// address, the given branch, and rport (RFC 3581) so a NATed probe still
// gets responses on UDP.
func (f *sipFlow) viaValue(branch string) string {
	return fmt.Sprintf("SIP/2.0/%s %s;branch=%s;rport", strings.ToUpper(f.transport), f.sentBy(), branch)
}

// contactValue is the Contact for a request on this flow. A sips: Request-URI
// requires a sips: Contact (RFC 3261 §8.1.1.8); RFC 5630 retires
// transport=tls, so a sips: Contact carries no transport parameter.
func (f *sipFlow) contactValue(requestURI string) string {
	if len(requestURI) >= 5 && strings.EqualFold(requestURI[:5], "sips:") {
		return fmt.Sprintf("<sips:probara@%s>", f.sentBy())
	}
	return fmt.Sprintf("<sip:probara@%s;transport=%s>", f.sentBy(), f.transport)
}

// sipFlowReadError is what a transaction returns when its flow ended before
// a final response arrived; it wraps the flow's own error.
func sipFlowReadError(f *sipFlow) error {
	err := f.Err()
	if errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("connection closed: %w", err)
	}
	return fmt.Errorf("failed to read response: %w", err)
}
