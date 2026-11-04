package email

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net"
	netmail "net/mail"
	"strconv"
	"strings"
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

func TestSMTPMailer_SendMessage(t *testing.T) {
	addr, received := fakeSMTPServer(t)
	host, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	mailer, err := NewSMTPMailer(SMTPParams{
		Host: host, Port: port, From: "noreply@probara.example.com", FromName: "Probara",
		DefaultRecipient: "alerts-default@example.com",
	})
	if err != nil {
		t.Fatalf("NewSMTPMailer: %v", err)
	}
	var sender mail.Sender = mailer

	err = sender.SendMessage(context.Background(), mail.Message{
		To:          []string{"Jane Doe <jane@example.com>"},
		Subject:     "You're invited",
		Text:        "Join the workspace",
		Attachments: []mail.Attachment{{Filename: "a.txt", ContentType: "text/plain; charset=utf-8", Data: []byte("hi")}},
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	var raw string
	select {
	case raw = <-received:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the message")
	}
	msg, err := netmail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parse message: %v", err)
	}
	to, err := msg.Header.AddressList("To")
	if err != nil || len(to) != 1 || to[0].Address != "jane@example.com" || to[0].Name != "Jane Doe" {
		t.Fatalf("To = %v (%v)", to, err)
	}
	mediaType, params, _ := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if mediaType != "multipart/mixed" {
		t.Fatalf("media type = %q", mediaType)
	}
	parts := readParts(t, msg.Body, params["boundary"])
	if len(parts) != 2 || parts[0].mediaType != "text/plain" || string(parts[0].body) != "Join the workspace" {
		t.Fatalf("parts = %+v", parts)
	}
	if parts[1].mediaType != "text/plain" || parts[1].params["charset"] != "utf-8" || string(parts[1].body) != "hi" {
		t.Errorf("attachment = %+v", parts[1])
	}
}

// An invalid message is refused before dialling, and transactional mail never
// falls back to the alert default recipients.
func TestSMTPMailer_SendMessageRejectsInvalidWithoutDialling(t *testing.T) {
	mailer, err := NewSMTPMailer(SMTPParams{
		Host: "127.0.0.1", Port: 1, From: "noreply@probara.example.com",
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
		"address injection": {To: []string{"a@example.com\r\nRCPT TO:<evil@example.com>"}, Subject: "s", Text: "t"},
	} {
		if err := mailer.SendMessage(context.Background(), msg); !errors.Is(err, mail.ErrInvalidMessage) {
			t.Errorf("%s: SendMessage = %v, want ErrInvalidMessage", name, err)
		}
	}
}
