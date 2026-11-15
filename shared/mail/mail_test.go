package mail

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func validMessage() Message {
	return Message{
		To:      []string{"ops@example.com", "Ops Team <team@example.com>"},
		Subject: "Your monthly report",
		Text:    "hello",
		Headers: map[string]string{
			"List-Unsubscribe":      "<https://status.example.com/unsubscribe?t=abc>",
			"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
		},
		Attachments: []Attachment{{Filename: "report.pdf", ContentType: "application/pdf", Data: []byte("%PDF")}},
	}
}

func TestValidateAcceptsWellFormedMessage(t *testing.T) {
	if err := validMessage().Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	m := validMessage()
	m.Subject = "Rapport mensuel — été\tà jour"
	m.Headers = nil
	m.Attachments = nil
	if err := m.Validate(); err != nil {
		t.Fatalf("non-ASCII subject with a tab: %v", err)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]func(*Message){
		"no recipients":             func(m *Message) { m.To = nil },
		"unparseable recipient":     func(m *Message) { m.To = []string{"not an address"} },
		"CRLF in recipient":         func(m *Message) { m.To = []string{"ops@example.com\r\nBcc: x@evil.example"} },
		"LF in recipient name":      func(m *Message) { m.To = []string{"Ops\n <ops@example.com>"} },
		"LF in subject":             func(m *Message) { m.Subject = "hi\nBcc: x@evil.example" },
		"CR in subject":             func(m *Message) { m.Subject = "hi\rthere" },
		"NEL in subject":            func(m *Message) { m.Subject = "hi\u0085Bcc: x@evil.example" },
		"no text body":              func(m *Message) { m.Text = "  " },
		"CRLF in header value":      func(m *Message) { m.Headers["List-Unsubscribe"] = "<x>\r\nBcc: x@evil.example" },
		"LF in header value":        func(m *Message) { m.Headers["List-Unsubscribe"] = "<x>\nBcc: x@evil.example" },
		"colon in header name":      func(m *Message) { m.Headers["X-Bad:Name"] = "v" },
		"space in header name":      func(m *Message) { m.Headers["X Bad"] = "v" },
		"CRLF in header name":       func(m *Message) { m.Headers["X-A\r\nBcc"] = "v" },
		"empty header name":         func(m *Message) { m.Headers[""] = "v" },
		"reserved header":           func(m *Message) { m.Headers["bcc"] = "x@evil.example" },
		"reserved content type":     func(m *Message) { m.Headers["Content-Type"] = "text/plain" },
		"reserved to":               func(m *Message) { m.Headers["TO"] = "x@evil.example" },
		"duplicate header by case":  func(m *Message) { m.Headers["list-unsubscribe"] = "<y>" },
		"attachment without name":   func(m *Message) { m.Attachments[0].Filename = "" },
		"CRLF in attachment name":   func(m *Message) { m.Attachments[0].Filename = "a.pdf\r\nX: y" },
		"bad attachment media type": func(m *Message) { m.Attachments[0].ContentType = "not a type" },
		"CRLF in attachment ct":     func(m *Message) { m.Attachments[0].ContentType = "text/plain\r\nX: y" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m := validMessage()
			mutate(&m)
			err := m.Validate()
			if !errors.Is(err, ErrInvalidMessage) {
				t.Fatalf("Validate() = %v, want ErrInvalidMessage", err)
			}
		})
	}
}

func TestDeliveryError(t *testing.T) {
	refused := errors.New("550 mailbox unavailable")
	err := error(&DeliveryError{Failed: []RecipientFailure{
		{Recipient: "a@example.com", Err: refused},
		{Recipient: "B <b@example.com>", Err: context.Canceled},
	}})
	var de *DeliveryError
	if !errors.As(err, &de) {
		t.Fatal("errors.As did not find the DeliveryError")
	}
	if got := de.Recipients(); !reflect.DeepEqual(got, []string{"a@example.com", "B <b@example.com>"}) {
		t.Errorf("Recipients = %v", got)
	}
	if !errors.Is(err, refused) || !errors.Is(err, context.Canceled) {
		t.Error("each recipient's error must be reachable through errors.Is")
	}
	if msg := err.Error(); !strings.Contains(msg, "2 recipient(s)") || !strings.Contains(msg, "a@example.com: 550 mailbox unavailable") {
		t.Errorf("Error() = %q", msg)
	}
}
