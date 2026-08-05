package email

import (
	"bufio"
	"context"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"strconv"
	"strings"
	"testing"
	"time"
)

// decodePart returns the decoded body of a base64 MIME part.
func decodePart(t *testing.T, part string) string {
	t.Helper()
	idx := strings.Index(part, "\r\n\r\n")
	if idx < 0 {
		t.Fatalf("part has no header/body separator: %q", part)
	}
	payload := strings.ReplaceAll(strings.TrimSpace(part[idx+4:]), "\r\n", "")
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatalf("decode part: %v", err)
	}
	return string(decoded)
}

func TestBuildEmailMessage_MultipartAlternative(t *testing.T) {
	msg := buildEmailMessage(emailMessage{
		From:     "alerts@probara.example.com",
		FromName: "Probara Alerts",
		To:       []string{"a@example.com", "b@example.com"},
		Subject:  "[Alert Triggered] API health",
		Text:     "plain body",
		HTML:     "<p>rich body</p>",
	})

	headers, rest, ok := strings.Cut(msg, "\r\n\r\n")
	if !ok {
		t.Fatal("message has no header block")
	}
	for _, want := range []string{
		"From: Probara Alerts <alerts@probara.example.com>",
		"To: a@example.com, b@example.com",
		"Subject: [Alert Triggered] API health",
		"MIME-Version: 1.0",
		"Content-Type: multipart/alternative; boundary=",
		"Auto-Submitted: auto-generated",
		"Message-ID: <",
		"Date: ",
	} {
		if !strings.Contains(headers, want) {
			t.Errorf("headers missing %q\n%s", want, headers)
		}
	}
	if !strings.Contains(headers, "@probara.example.com>") {
		t.Error("Message-ID should use the sender domain")
	}

	boundary := ""
	for _, line := range strings.Split(headers, "\r\n") {
		if strings.HasPrefix(line, "Content-Type: multipart/alternative") {
			_, params, err := mime.ParseMediaType(strings.TrimPrefix(line, "Content-Type: "))
			if err != nil {
				t.Fatalf("parse content type: %v", err)
			}
			boundary = params["boundary"]
		}
	}
	if boundary == "" {
		t.Fatal("no boundary parsed")
	}

	parts := strings.Split(rest, "--"+boundary)
	// parts[0] is the empty preamble, then text, then html, then the "--" close.
	if len(parts) != 4 {
		t.Fatalf("expected 2 body parts, got %d chunks: %q", len(parts)-2, parts)
	}
	if !strings.Contains(parts[1], "text/plain") {
		t.Error("first part must be text/plain (least rich first)")
	}
	if got := decodePart(t, parts[1]); got != "plain body" {
		t.Errorf("text part = %q", got)
	}
	if !strings.Contains(parts[2], "text/html") {
		t.Error("second part must be text/html")
	}
	if got := decodePart(t, parts[2]); got != "<p>rich body</p>" {
		t.Errorf("html part = %q", got)
	}
	if !strings.HasSuffix(strings.TrimSpace(msg), "--"+boundary+"--") {
		t.Error("message must end with the closing boundary")
	}
}

func TestBuildEmailMessage_SinglePartWhenNoHTML(t *testing.T) {
	msg := buildEmailMessage(emailMessage{
		From:    "alerts@probara.example.com",
		To:      []string{"a@example.com"},
		Subject: "s",
		Text:    "plain only",
	})
	if strings.Contains(msg, "multipart/alternative") {
		t.Error("expected a single-part message")
	}
	if !strings.Contains(msg, "Content-Type: text/plain; charset=\"UTF-8\"") {
		t.Error("missing text/plain content type")
	}
	if !strings.Contains(msg, "From: alerts@probara.example.com\r\n") {
		t.Error("bare address expected when no display name is set")
	}
	if got := decodePart(t, msg); got != "plain only" {
		t.Errorf("body = %q", got)
	}
}

func TestBuildEmailMessage_EncodesNonASCIIHeaders(t *testing.T) {
	msg := buildEmailMessage(emailMessage{
		From:     "alerts@probara.example.com",
		FromName: "Probara Alertes",
		To:       []string{"a@example.com"},
		Subject:  "[Alerte] Téléphonie SIP · 6.4σ",
		Text:     "corps",
	})
	subject := ""
	for _, line := range strings.Split(msg, "\r\n") {
		if strings.HasPrefix(line, "Subject: ") {
			subject = strings.TrimPrefix(line, "Subject: ")
		}
	}
	if !strings.HasPrefix(subject, "=?utf-8?") {
		t.Fatalf("non-ASCII subject must be RFC 2047 encoded, got %q", subject)
	}
	decoded, err := new(mime.WordDecoder).DecodeHeader(subject)
	if err != nil {
		t.Fatalf("decode subject: %v", err)
	}
	if decoded != "[Alerte] Téléphonie SIP · 6.4σ" {
		t.Errorf("round-trip subject = %q", decoded)
	}
}

func TestBase64Body_WrapsAtSeventySixColumns(t *testing.T) {
	encoded := base64Body(strings.Repeat("probara alert body ", 40))
	for _, line := range strings.Split(encoded, "\r\n") {
		if len(line) > 76 {
			t.Fatalf("line exceeds 76 columns (%d): %q", len(line), line)
		}
	}
	joined := strings.ReplaceAll(encoded, "\r\n", "")
	decoded, err := base64.StdEncoding.DecodeString(joined)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(decoded) != strings.Repeat("probara alert body ", 40) {
		t.Error("round-trip mismatch")
	}
}

// fakeSMTPServer speaks just enough SMTP to capture one DATA payload, so the
// test can assert that what actually goes on the wire parses as a real MIME
// message rather than only checking the builder's return value.
func fakeSMTPServer(t *testing.T) (addr string, received <-chan string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })

	out := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		write := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }

		write("220 fake ESMTP")
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
					out <- body.String()
					write("250 queued")
					continue
				}
				body.WriteString(line)
				continue
			}
			switch verb := strings.ToUpper(strings.Fields(line + " ")[0]); verb {
			case "EHLO", "HELO":
				write("250 fake")
			case "MAIL", "RCPT":
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
	return listener.Addr().String(), out
}

func TestSMTPMailer_SendAlertProducesParseableMIME(t *testing.T) {
	addr, received := fakeSMTPServer(t)
	host, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)

	mailer, err := NewSMTPMailer(SMTPParams{
		Host:       host,
		Port:       port,
		From:       "alerts@probara.example.com",
		FromName:   "Probara Alerts",
		AppBaseURL: "https://probara.example.com",
	})
	if err != nil {
		t.Fatalf("NewSMTPMailer: %v", err)
	}

	event := sampleEvent()
	event.Alert.MonitorID = "3f2a91cc-11de-4b7a-9a2e-6c5f8d0e1234"
	if err := mailer.SendAlert(context.Background(), event, Templates{}, []string{"ops@example.com"}); err != nil {
		t.Fatalf("SendAlert: %v", err)
	}

	var raw string
	select {
	case raw = <-received:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the message")
	}

	msg, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parse message: %v", err)
	}
	if got := msg.Header.Get("Subject"); got != "[Alert Triggered] API health" {
		t.Errorf("Subject = %q", got)
	}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("parse content type: %v", err)
	}
	if mediaType != "multipart/alternative" {
		t.Fatalf("media type = %q, want multipart/alternative", mediaType)
	}

	reader := multipart.NewReader(msg.Body, params["boundary"])
	var types []string
	var bodies []string
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("next part: %v", err)
		}
		types = append(types, part.Header.Get("Content-Type"))
		decoded, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, part))
		if err != nil {
			t.Fatalf("read part: %v", err)
		}
		bodies = append(bodies, string(decoded))
	}
	if len(bodies) != 2 {
		t.Fatalf("expected 2 parts, got %d (%v)", len(bodies), types)
	}
	if !strings.Contains(types[0], "text/plain") || !strings.Contains(types[1], "text/html") {
		t.Errorf("part order = %v, want text/plain then text/html", types)
	}
	if strings.Contains(bodies[0], "<table") {
		t.Error("plain part contains markup")
	}
	for _, want := range []string{"<!DOCTYPE html", "API health", "https://probara.example.com/monitors/"} {
		if !strings.Contains(bodies[1], want) {
			t.Errorf("html part missing %q", want)
		}
	}
	// Every line must fit the SMTP 998-octet limit.
	for _, line := range strings.Split(raw, "\n") {
		if len(strings.TrimRight(line, "\r")) > 998 {
			t.Fatalf("line exceeds the SMTP limit: %d octets", len(line))
		}
	}
}

func TestCombineTemplateErrors(t *testing.T) {
	if err := combineTemplateErrors(nil, nil, nil); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
	err := combineTemplateErrors(nil, errTest("body broke"), errTest("html broke"))
	if err == nil || !strings.Contains(err.Error(), "body broke") || !strings.Contains(err.Error(), "html broke") {
		t.Errorf("expected both failures reported, got %v", err)
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }
