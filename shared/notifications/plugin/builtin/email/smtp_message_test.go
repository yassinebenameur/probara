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
	rcpt []string
	data string
}

// fakeMultiSMTPServer accepts any number of connections and records every
// completed transaction. RCPT for an address in reject gets a 550, so a test
// can fail one recipient of a fan-out.
func fakeMultiSMTPServer(t *testing.T, reject ...string) (host string, port int, transactions func() []smtpTransaction) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	rejected := map[string]bool{}
	for _, r := range reject {
		rejected[r] = true
	}

	// A transaction is recorded before its "250 queued" reply, so it is
	// visible as soon as the client's send returns.
	var mu sync.Mutex
	var done []smtpTransaction
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				reader := bufio.NewReader(conn)
				write := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
				write("220 fake ESMTP")
				var cur smtpTransaction
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
							mu.Lock()
							done = append(done, cur)
							mu.Unlock()
							cur, body = smtpTransaction{}, strings.Builder{}
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
					case "RCPT":
						addr := strings.TrimSuffix(strings.TrimPrefix(trimmed[strings.Index(trimmed, ":")+1:], "<"), ">")
						if rejected[addr] {
							write("550 mailbox unavailable")
							continue
						}
						cur.rcpt = append(cur.rcpt, addr)
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
	addr := listener.Addr().(*net.TCPAddr)
	return addr.IP.String(), addr.Port, func() []smtpTransaction {
		mu.Lock()
		defer mu.Unlock()
		return append([]smtpTransaction(nil), done...)
	}
}

func TestSMTPMailer_SendMessageOneCopyPerRecipient(t *testing.T) {
	host, port, transactions := fakeMultiSMTPServer(t)
	mailer, err := NewSMTPMailer(SMTPParams{
		Host: host, Port: port, From: "noreply@probara.example.com", FromName: "Probara",
		DefaultRecipient: "alerts-default@example.com",
	})
	if err != nil {
		t.Fatalf("NewSMTPMailer: %v", err)
	}
	var sender mail.Sender = mailer

	err = sender.SendMessage(context.Background(), mail.Message{
		// The duplicate (by case) must not produce a second copy.
		To:          []string{"Jane Doe <jane@example.com>", "bob@example.com", "JANE@example.com"},
		Subject:     "You're invited",
		Text:        "Join the workspace",
		Headers:     map[string]string{"List-Unsubscribe": "<https://probara.example.com/u?t=abc>"},
		Attachments: []mail.Attachment{{Filename: "a.txt", ContentType: "text/plain; charset=utf-8", Data: []byte("hi")}},
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	got := transactions()
	if len(got) != 2 {
		t.Fatalf("expected one transaction per distinct recipient (2), got %d", len(got))
	}
	wantTo := []struct{ address, name string }{{"jane@example.com", "Jane Doe"}, {"bob@example.com", ""}}
	for i, tx := range got {
		if len(tx.rcpt) != 1 || tx.rcpt[0] != wantTo[i].address {
			t.Errorf("transaction %d envelope recipients = %v, want only %s", i, tx.rcpt, wantTo[i].address)
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

// A refused recipient does not stop the others, and the error names exactly
// the recipients still owed a copy.
func TestSMTPMailer_SendMessagePartialFailure(t *testing.T) {
	host, port, transactions := fakeMultiSMTPServer(t, "bad@example.com")
	mailer, err := NewSMTPMailer(SMTPParams{Host: host, Port: port, From: "noreply@probara.example.com"})
	if err != nil {
		t.Fatalf("NewSMTPMailer: %v", err)
	}
	err = mailer.SendMessage(context.Background(), mail.Message{
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
	var delivered []string
	for _, tx := range transactions() {
		delivered = append(delivered, tx.rcpt...)
	}
	if strings.Join(delivered, ",") != "a@example.com,c@example.com" {
		t.Errorf("delivered to %v, want a@ and c@", delivered)
	}
}

// An invalid message is refused before dialling, and transactional mail never
// falls back to the alert default recipients.
func TestSMTPMailer_SendMessageRejectsInvalidWithoutDialling(t *testing.T) {
	host, port, transactions := fakeMultiSMTPServer(t)
	mailer, err := NewSMTPMailer(SMTPParams{
		Host: host, Port: port, From: "noreply@probara.example.com",
		DefaultRecipient: "alerts-default@example.com",
	})
	if err != nil {
		t.Fatalf("NewSMTPMailer: %v", err)
	}
	for name, msg := range map[string]mail.Message{
		"no recipients":     {Subject: "s", Text: "t"},
		"subject injection": {To: []string{"a@example.com"}, Subject: "s\r\nBcc: evil@example.com", Text: "t"},
		"header injection": {To: []string{"a@example.com"}, Subject: "s", Text: "t",
			Headers: map[string]string{"List-Unsubscribe": "<x>\nBcc: evil@example.com"}},
		"address injection":        {To: []string{"a@example.com\r\nRCPT TO:<evil@example.com>"}, Subject: "s", Text: "t"},
		"second address injection": {To: []string{"a@example.com", "b@example.com\nBcc: evil@example.com"}, Subject: "s", Text: "t"},
		"reserved header":          {To: []string{"a@example.com"}, Subject: "s", Text: "t", Headers: map[string]string{"Bcc": "evil@example.com"}},
	} {
		if err := mailer.SendMessage(context.Background(), msg); !errors.Is(err, mail.ErrInvalidMessage) {
			t.Errorf("%s: SendMessage = %v, want ErrInvalidMessage", name, err)
		}
	}
	if got := transactions(); len(got) != 0 {
		t.Errorf("invalid messages reached the server: %d transactions", len(got))
	}
}

func TestSMTPMailer_SendMessageHonoursCancelledContext(t *testing.T) {
	host, port, transactions := fakeMultiSMTPServer(t)
	mailer, err := NewSMTPMailer(SMTPParams{Host: host, Port: port, From: "noreply@probara.example.com"})
	if err != nil {
		t.Fatalf("NewSMTPMailer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = mailer.SendMessage(ctx, mail.Message{To: []string{"a@example.com"}, Subject: "s", Text: "t"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("SendMessage = %v, want context.Canceled", err)
	}
	if got := transactions(); len(got) != 0 {
		t.Errorf("a cancelled send reached the server: %d transactions", len(got))
	}
}
