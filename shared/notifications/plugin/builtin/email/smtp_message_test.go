package email

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net"
	netmail "net/mail"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/mail"
)

type parsedPart struct {
	mediaType   string
	params      map[string]string
	disposition string
	dispParams  map[string]string
	body        []byte
}

// readParts walks a multipart body, base64-decoding every leaf.
func readParts(t *testing.T, body io.Reader, boundary string) []parsedPart {
	t.Helper()
	reader := multipart.NewReader(body, boundary)
	var parts []parsedPart
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			return parts
		}
		if err != nil {
			t.Fatalf("next part: %v", err)
		}
		mediaType, params, err := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if err != nil {
			t.Fatalf("parse part content type: %v", err)
		}
		p := parsedPart{mediaType: mediaType, params: params}
		if cd := part.Header.Get("Content-Disposition"); cd != "" {
			p.disposition, p.dispParams, err = mime.ParseMediaType(cd)
			if err != nil {
				t.Fatalf("parse disposition: %v", err)
			}
		}
		if strings.HasPrefix(mediaType, "multipart/") {
			raw, _ := io.ReadAll(part)
			p.body = raw
		} else {
			if enc := part.Header.Get("Content-Transfer-Encoding"); enc != "base64" {
				t.Fatalf("part %s Content-Transfer-Encoding = %q, want base64", mediaType, enc)
			}
			p.body, err = io.ReadAll(base64.NewDecoder(base64.StdEncoding, part))
			if err != nil {
				t.Fatalf("decode part: %v", err)
			}
		}
		parts = append(parts, p)
	}
}

func TestBuildEmailMessage_AttachmentsUseMultipartMixed(t *testing.T) {
	pdf := []byte("%PDF-1.7\x00\xff\xfe binary \r\n payload")
	raw := buildEmailMessage(emailMessage{
		From:    "reports@probara.example.com",
		To:      []string{"ops@example.com"},
		Subject: "Rapport mensuel — été",
		Text:    "plain body",
		HTML:    "<p>rich body</p>",
		Headers: map[string]string{
			"list-unsubscribe":      "<https://status.example.com/u?t=abc>, <mailto:unsub@example.com>",
			"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
		},
		Attachments: []mail.Attachment{
			{Filename: "report.pdf", ContentType: "application/pdf", Data: pdf},
			{Filename: "données.csv", Data: []byte("a,b\n1,2\n")},
		},
	})

	msg, err := netmail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parse message: %v", err)
	}
	dec := new(mime.WordDecoder)
	if subject, _ := dec.DecodeHeader(msg.Header.Get("Subject")); subject != "Rapport mensuel — été" {
		t.Errorf("Subject = %q", subject)
	}
	if got := msg.Header.Get("List-Unsubscribe"); got != "<https://status.example.com/u?t=abc>, <mailto:unsub@example.com>" {
		t.Errorf("List-Unsubscribe = %q", got)
	}
	if got := msg.Header.Get("List-Unsubscribe-Post"); got != "List-Unsubscribe=One-Click" {
		t.Errorf("List-Unsubscribe-Post = %q", got)
	}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/mixed" {
		t.Fatalf("top-level type = %q (%v), want multipart/mixed", mediaType, err)
	}

	outer := readParts(t, msg.Body, params["boundary"])
	if len(outer) != 3 {
		t.Fatalf("expected body + 2 attachments, got %d parts", len(outer))
	}
	if outer[0].mediaType != "multipart/alternative" {
		t.Fatalf("first part = %q, want multipart/alternative", outer[0].mediaType)
	}
	inner := readParts(t, bytes.NewReader(outer[0].body), outer[0].params["boundary"])
	if len(inner) != 2 || inner[0].mediaType != "text/plain" || inner[1].mediaType != "text/html" {
		t.Fatalf("alternative parts = %+v", inner)
	}
	if string(inner[0].body) != "plain body" || string(inner[1].body) != "<p>rich body</p>" {
		t.Errorf("bodies = %q / %q", inner[0].body, inner[1].body)
	}

	if outer[1].mediaType != "application/pdf" || outer[1].disposition != "attachment" || outer[1].dispParams["filename"] != "report.pdf" {
		t.Errorf("pdf part = %+v", outer[1])
	}
	if !bytes.Equal(outer[1].body, pdf) {
		t.Errorf("pdf bytes did not round-trip: %q", outer[1].body)
	}
	if outer[2].mediaType != "application/octet-stream" || outer[2].dispParams["filename"] != "données.csv" {
		t.Errorf("csv part = %+v", outer[2])
	}
	if string(outer[2].body) != "a,b\n1,2\n" {
		t.Errorf("csv bytes = %q", outer[2].body)
	}
	for _, line := range strings.Split(raw, "\n") {
		if len(strings.TrimRight(line, "\r")) > 998 {
			t.Fatalf("line exceeds the SMTP limit: %d octets", len(line))
		}
	}
}

func TestBuildEmailMessage_NoAttachmentsStaysAlternative(t *testing.T) {
	raw := buildEmailMessage(emailMessage{
		From: "a@probara.example.com", To: []string{"b@example.com"}, Subject: "s", Text: "t", HTML: "<p>h</p>",
		Headers: map[string]string{"List-Unsubscribe": "<https://x.example/u>"},
	})
	msg, err := netmail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parse message: %v", err)
	}
	mediaType, _, _ := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if mediaType != "multipart/alternative" {
		t.Fatalf("media type = %q, want multipart/alternative", mediaType)
	}
	if msg.Header.Get("List-Unsubscribe") != "<https://x.example/u>" {
		t.Errorf("List-Unsubscribe header missing")
	}
}

// smtpTransaction is one MAIL..DATA exchange the fake server accepted.
type smtpTransaction struct {
	conn int // 1-based connection number
	rcpt []string
	data string
}

// fakeSMTPOptions shape the fake server's misbehaviour. Counts are 1-based
// and global across connections; zero disables the behaviour.
type fakeSMTPOptions struct {
	reject        []string // RCPT for these addresses gets a 550
	dropAtMail    int      // close the connection on the Nth MAIL, unanswered
	busyAtMail    int      // answer the Nth MAIL with 421 and close
	dropAfterData int      // record the Nth message, then close without the 250
}

type fakeSMTP struct {
	host string
	port int

	mu           sync.Mutex
	connections  int
	transactions []smtpTransaction
}

func (f *fakeSMTP) snapshot() (connections int, transactions []smtpTransaction) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connections, append([]smtpTransaction(nil), f.transactions...)
}

// delivered lists every envelope recipient of a recorded transaction, in
// order.
func (f *fakeSMTP) delivered() []string {
	_, txs := f.snapshot()
	var out []string
	for _, tx := range txs {
		out = append(out, tx.rcpt...)
	}
	return out
}

// startFakeSMTP accepts any number of connections and records every
// completed transaction. A transaction is recorded before its "250 queued"
// reply, so it is visible as soon as the client's send returns.
func startFakeSMTP(t *testing.T, opts fakeSMTPOptions) *fakeSMTP {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	rejected := map[string]bool{}
	for _, r := range opts.reject {
		rejected[r] = true
	}
	f := &fakeSMTP{}
	addr := listener.Addr().(*net.TCPAddr)
	f.host, f.port = addr.IP.String(), addr.Port
	mails, datas := 0, 0

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			f.mu.Lock()
			f.connections++
			connNo := f.connections
			f.mu.Unlock()
			go func() {
				defer conn.Close()
				reader := bufio.NewReader(conn)
				write := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
				write("220 fake ESMTP")
				cur := smtpTransaction{conn: connNo}
				var body strings.Builder
				inData := false
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					if inData {
						if strings.TrimRight(line, "\r\n") == "." {
							inData = false
							cur.data = body.String()
							f.mu.Lock()
							f.transactions = append(f.transactions, cur)
							datas++
							drop := datas == opts.dropAfterData
							f.mu.Unlock()
							cur, body = smtpTransaction{conn: connNo}, strings.Builder{}
							if drop {
								return
							}
							write("250 queued")
							continue
						}
						body.WriteString(line)
						continue
					}
					trimmed := strings.TrimRight(line, "\r\n")
					switch verb := strings.ToUpper(strings.Fields(trimmed + " ")[0]); verb {
					case "EHLO", "HELO":
						write("250 fake")
					case "MAIL":
						f.mu.Lock()
						mails++
						n := mails
						f.mu.Unlock()
						if n == opts.dropAtMail {
							return
						}
						if n == opts.busyAtMail {
							write("421 too many messages, closing")
							return
						}
						cur = smtpTransaction{conn: connNo}
						write("250 ok")
					case "RCPT":
						addr := strings.TrimSuffix(strings.TrimPrefix(trimmed[strings.Index(trimmed, ":")+1:], "<"), ">")
						if rejected[addr] {
							write("550 mailbox unavailable")
							continue
						}
						cur.rcpt = append(cur.rcpt, addr)
						write("250 ok")
					case "RSET":
						cur = smtpTransaction{conn: connNo}
						write("250 ok")
					case "DATA":
						inData = true
						write("354 send it")
					case "QUIT":
						write("221 bye")
						return
					default:
						write("250 ok")
					}
				}
			}()
		}
	}()
	return f
}

func newTestMailer(t *testing.T, f *fakeSMTP, params SMTPParams) *SMTPMailer {
	t.Helper()
	params.Host, params.Port = f.host, f.port
	if params.From == "" {
		params.From = "noreply@probara.example.com"
	}
	mailer, err := NewSMTPMailer(params)
	if err != nil {
		t.Fatalf("NewSMTPMailer: %v", err)
	}
	return mailer
}

func TestSMTPMailer_SendMessageOneCopyPerRecipient(t *testing.T) {
	f := startFakeSMTP(t, fakeSMTPOptions{})
	mailer := newTestMailer(t, f, SMTPParams{FromName: "Probara", DefaultRecipient: "alerts-default@example.com"})
	var sender mail.Sender = mailer

	longName := strings.Repeat("Élodie Müller ", 25)
	err := sender.SendMessage(context.Background(), mail.Message{
		// The duplicate (by case) must not produce a second copy.
		To:          []string{"Jane Doe <jane@example.com>", "bob@example.com", "JANE@example.com", strings.TrimSpace(longName) + " <elodie@example.com>"},
		Subject:     "You're invited",
		Text:        "Join the workspace",
		Headers:     map[string]string{"List-Unsubscribe": "<https://probara.example.com/u?t=abc>"},
		Attachments: []mail.Attachment{{Filename: "a.txt", ContentType: "text/plain; charset=utf-8", Data: []byte("hi")}},
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	connections, got := f.snapshot()
	if connections != 1 {
		t.Errorf("used %d connections for 3 recipients, want 1", connections)
	}
	wantTo := []struct{ address, name string }{
		{"jane@example.com", "Jane Doe"}, {"bob@example.com", ""}, {"elodie@example.com", strings.TrimSpace(longName)},
	}
	if len(got) != len(wantTo) {
		t.Fatalf("expected one transaction per distinct recipient (%d), got %d", len(wantTo), len(got))
	}
	for i, tx := range got {
		if len(tx.rcpt) != 1 || tx.rcpt[0] != wantTo[i].address {
			t.Errorf("transaction %d envelope recipients = %v, want only %s", i, tx.rcpt, wantTo[i].address)
		}
		for _, line := range strings.Split(tx.data, "\n") {
			if len(strings.TrimRight(line, "\r")) > 998 {
				t.Fatalf("message %d has a %d-octet line", i, len(line))
			}
		}
		msg, err := netmail.ReadMessage(strings.NewReader(tx.data))
		if err != nil {
			t.Fatalf("parse message %d: %v", i, err)
		}
		to, err := msg.Header.AddressList("To")
		if err != nil || len(to) != 1 || to[0].Address != wantTo[i].address || to[0].Name != wantTo[i].name {
			t.Errorf("message %d To = %v (%v), want only %s", i, to, err, wantTo[i].address)
		}
		if strings.Contains(tx.data, "alerts-default@example.com") {
			t.Errorf("message %d fell back to the alert default recipient", i)
		}
		if got := msg.Header.Get("List-Unsubscribe"); got != "<https://probara.example.com/u?t=abc>" {
			t.Errorf("message %d List-Unsubscribe = %q", i, got)
		}
		mediaType, params, _ := mime.ParseMediaType(msg.Header.Get("Content-Type"))
		if mediaType != "multipart/mixed" {
			t.Fatalf("message %d media type = %q", i, mediaType)
		}
		parts := readParts(t, msg.Body, params["boundary"])
		if len(parts) != 2 || parts[0].mediaType != "text/plain" || string(parts[0].body) != "Join the workspace" {
			t.Fatalf("message %d parts = %+v", i, parts)
		}
		if parts[1].mediaType != "text/plain" || parts[1].params["charset"] != "utf-8" || string(parts[1].body) != "hi" {
			t.Errorf("message %d attachment = %+v", i, parts[1])
		}
	}
}

// A refused recipient is RSET and the rest carry on over the same
// connection; the error names exactly the recipients still owed a copy.
func TestSMTPMailer_SendMessagePartialFailure(t *testing.T) {
	f := startFakeSMTP(t, fakeSMTPOptions{reject: []string{"bad@example.com"}})
	mailer := newTestMailer(t, f, SMTPParams{})
	err := mailer.SendMessage(context.Background(), mail.Message{
		To:      []string{"a@example.com", "bad@example.com", "c@example.com"},
		Subject: "Report",
		Text:    "body",
	})
	var de *mail.DeliveryError
	if !errors.As(err, &de) {
		t.Fatalf("SendMessage = %v, want a *mail.DeliveryError", err)
	}
	if got := de.Recipients(); len(got) != 1 || got[0] != "bad@example.com" {
		t.Errorf("failed recipients = %v, want [bad@example.com]", got)
	}
	if got := f.delivered(); strings.Join(got, ",") != "a@example.com,c@example.com" {
		t.Errorf("delivered to %v, want a@ and c@", got)
	}
	if connections, _ := f.snapshot(); connections != 1 {
		t.Errorf("a refused recipient must not cost a reconnect, used %d connections", connections)
	}
}

// A connection lost before the end of DATA cannot have delivered anything,
// so that recipient is retried on a new connection and everyone gets
// exactly one copy. A 421 at MAIL is the same case, announced.
func TestSMTPMailer_SendMessageReconnectsBeforeData(t *testing.T) {
	for name, opts := range map[string]fakeSMTPOptions{
		"dropped at MAIL": {dropAtMail: 2},
		"421 at MAIL":     {busyAtMail: 2},
	} {
		t.Run(name, func(t *testing.T) {
			f := startFakeSMTP(t, opts)
			mailer := newTestMailer(t, f, SMTPParams{})
			err := mailer.SendMessage(context.Background(), mail.Message{
				To: []string{"a@example.com", "b@example.com", "c@example.com"}, Subject: "s", Text: "t",
			})
			if err != nil {
				t.Fatalf("SendMessage: %v", err)
			}
			if got := f.delivered(); strings.Join(got, ",") != "a@example.com,b@example.com,c@example.com" {
				t.Errorf("delivered = %v, want each recipient once", got)
			}
			if connections, _ := f.snapshot(); connections != 2 {
				t.Errorf("connections = %d, want 2 (one reconnect)", connections)
			}
		})
	}
}

// A connection lost after the end of DATA may have delivered the message, so
// that recipient is reported, not re-sent; the rest go out on a new
// connection.
func TestSMTPMailer_SendMessageDoesNotResendAfterData(t *testing.T) {
	f := startFakeSMTP(t, fakeSMTPOptions{dropAfterData: 2})
	mailer := newTestMailer(t, f, SMTPParams{})
	err := mailer.SendMessage(context.Background(), mail.Message{
		To: []string{"a@example.com", "b@example.com", "c@example.com"}, Subject: "s", Text: "t",
	})
	var de *mail.DeliveryError
	if !errors.As(err, &de) {
		t.Fatalf("SendMessage = %v, want a *mail.DeliveryError", err)
	}
	if got := de.Recipients(); strings.Join(got, ",") != "b@example.com" {
		t.Errorf("failed recipients = %v, want [b@example.com]", got)
	}
	if got := f.delivered(); strings.Join(got, ",") != "a@example.com,b@example.com,c@example.com" {
		t.Errorf("server received %v, want each recipient exactly once", got)
	}
}

// An invalid message is refused before dialling, and transactional mail never
// falls back to the alert default recipients.
func TestSMTPMailer_SendMessageRejectsInvalidWithoutDialling(t *testing.T) {
	f := startFakeSMTP(t, fakeSMTPOptions{})
	mailer := newTestMailer(t, f, SMTPParams{DefaultRecipient: "alerts-default@example.com"})
	for name, msg := range map[string]mail.Message{
		"no recipients":     {Subject: "s", Text: "t"},
		"subject injection": {To: []string{"a@example.com"}, Subject: "s\r\nBcc: evil@example.com", Text: "t"},
		"header injection": {To: []string{"a@example.com"}, Subject: "s", Text: "t",
			Headers: map[string]string{"List-Unsubscribe": "<x>\nBcc: evil@example.com"}},
		"address injection":           {To: []string{"a@example.com\r\nRCPT TO:<evil@example.com>"}, Subject: "s", Text: "t"},
		"second address injection":    {To: []string{"a@example.com", "b@example.com\nBcc: evil@example.com"}, Subject: "s", Text: "t"},
		"quoted local part smuggling": {To: []string{`"attacker@evil.example>"@corp.example`}, Subject: "s", Text: "t"},
		"reserved header":             {To: []string{"a@example.com"}, Subject: "s", Text: "t", Headers: map[string]string{"Bcc": "evil@example.com"}},
	} {
		if err := mailer.SendMessage(context.Background(), msg); !errors.Is(err, mail.ErrInvalidMessage) {
			t.Errorf("%s: SendMessage = %v, want ErrInvalidMessage", name, err)
		}
	}
	if connections, _ := f.snapshot(); connections != 0 {
		t.Errorf("invalid messages reached the server: %d connections", connections)
	}
}

func TestSMTPMailer_SendMessageHonoursCancelledContext(t *testing.T) {
	f := startFakeSMTP(t, fakeSMTPOptions{})
	mailer := newTestMailer(t, f, SMTPParams{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := mailer.SendMessage(ctx, mail.Message{To: []string{"a@example.com"}, Subject: "s", Text: "t"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("SendMessage = %v, want context.Canceled", err)
	}
	if connections, _ := f.snapshot(); connections != 0 {
		t.Errorf("a cancelled send reached the server: %d connections", connections)
	}
}

// silentSMTPServer accepts connections and never says a word.
func silentSMTPServer(t *testing.T) (string, int) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var mu sync.Mutex
	var conns []net.Conn
	t.Cleanup(func() {
		listener.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			c.Close()
		}
	})
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
		}
	}()
	addr := listener.Addr().(*net.TCPAddr)
	return addr.IP.String(), addr.Port
}

// A server that accepts and goes silent must not hold a send forever: the
// ctx deadline bounds it, cancelling ctx interrupts it, and without either
// the per-message default (shortened here) does.
func TestSMTPMailer_SilentServerIsBounded(t *testing.T) {
	host, port := silentSMTPServer(t)
	mailer, err := NewSMTPMailer(SMTPParams{Host: host, Port: port, From: "noreply@probara.example.com"})
	if err != nil {
		t.Fatalf("NewSMTPMailer: %v", err)
	}
	msg := mail.Message{To: []string{"a@example.com", "b@example.com"}, Subject: "s", Text: "t"}
	within := func(name string, limit time.Duration, send func() error) {
		t.Helper()
		start := time.Now()
		err := send()
		if err == nil {
			t.Errorf("%s: send to a silent server succeeded", name)
		}
		if elapsed := time.Since(start); elapsed > limit {
			t.Errorf("%s: took %v, want under %v", name, elapsed, limit)
		}
	}

	within("ctx deadline", 3*time.Second, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		err := mailer.SendMessage(ctx, msg)
		var de *mail.DeliveryError
		if !errors.As(err, &de) || len(de.Failed) != 2 {
			t.Errorf("ctx deadline: SendMessage = %v, want both recipients failed", err)
		}
		return err
	})

	within("ctx cancelled mid-send", 3*time.Second, func() error {
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(300*time.Millisecond, cancel)
		return mailer.SendMessage(ctx, msg)
	})

	saved := smtpMessageTimeout
	smtpMessageTimeout = 300 * time.Millisecond
	t.Cleanup(func() { smtpMessageTimeout = saved })
	within("default bound, alert path", 3*time.Second, func() error {
		return mailer.SendAlert(context.Background(), sampleEvent(), Templates{}, []string{"ops@example.com"})
	})
}

// headerLines returns the raw lines (first plus continuations) of one header.
func headerLines(raw, name string) []string {
	head, _, _ := strings.Cut(raw, "\r\n\r\n")
	var out []string
	in := false
	for _, line := range strings.Split(head, "\r\n") {
		switch {
		case strings.HasPrefix(line, name+": "):
			in = true
			out = append(out, line)
		case in && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")):
			out = append(out, line)
		default:
			in = false
		}
	}
	return out
}

// The encoder never folds, so a long subject or filename used to come out as
// one line of several thousand octets. Every line must stay within the SMTP
// limit, the subject's continuation lines within the recommended 78, and both
// must decode back to exactly what was given.
func TestBuildEmailMessage_LongHeadersFoldAndRoundTrip(t *testing.T) {
	filename := strings.Repeat("é", 120) + ".pdf"
	for name, subject := range map[string]string{
		"non-ASCII":            strings.Repeat("Rapport mensuel — été à jour ", 21),
		"ASCII without spaces": strings.Repeat("a", 1200),
	} {
		t.Run(name, func(t *testing.T) {
			raw := buildEmailMessage(emailMessage{
				From: "reports@probara.example.com", FromName: "Probara Überwachung",
				To: []string{"ops@example.com"}, Subject: subject, Text: "t",
				Attachments: []mail.Attachment{{Filename: filename, ContentType: "application/pdf", Data: []byte("%PDF")}},
			})
			for _, line := range strings.Split(raw, "\n") {
				if n := len(strings.TrimRight(line, "\r")); n > 998 {
					t.Fatalf("line of %d octets exceeds the SMTP limit: %.80q…", n, line)
				}
			}
			lines := headerLines(raw, "Subject")
			if len(lines) < 2 {
				t.Fatalf("a long subject must be folded, got %d line(s)", len(lines))
			}
			if len(lines[0]) > len("Subject: ")+75 {
				t.Errorf("first subject line is %d octets", len(lines[0]))
			}
			for _, line := range lines[1:] {
				if len(line) > 78 {
					t.Errorf("subject continuation line is %d octets: %q", len(line), line)
				}
			}

			msg, err := netmail.ReadMessage(strings.NewReader(raw))
			if err != nil {
				t.Fatalf("parse message: %v", err)
			}
			got, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
			if err != nil || got != subject {
				t.Errorf("subject did not round-trip (%v):\n got %q\nwant %q", err, got, subject)
			}
			_, params, _ := mime.ParseMediaType(msg.Header.Get("Content-Type"))
			parts := readParts(t, msg.Body, params["boundary"])
			if len(parts) != 2 || parts[1].dispParams["filename"] != filename {
				t.Errorf("filename did not round-trip: %+v", parts)
			}
		})
	}
}

// Short headers are written exactly as before: folding only starts where the
// encoder had to split the value into several encoded words.
func TestEncodeHeader_ShortValuesUnchanged(t *testing.T) {
	for _, v := range []string{"[Alert Triggered] API health", "[Alerte] Téléphonie SIP · 6.4σ", strings.Repeat("x", maxRawHeaderValue)} {
		if got, want := encodeHeader(v), mime.QEncoding.Encode("utf-8", v); got != want {
			t.Errorf("encodeHeader(%q) = %q, want %q", v, got, want)
		}
	}
}
