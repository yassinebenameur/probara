package bootstrap

import (
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/mail"
	"github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/email"
)

// MailSender returns the transactional mailer (shared/mail) over the same
// SMTP_* settings the email plugin uses, or nil when SMTP is not configured.
// Unlike the alert mailer it has no default recipients: transactional mail
// always names its own.
func MailSender(smtp config.SMTPConfig) (mail.Sender, error) {
	if smtp.SMTPHost == "" || smtp.SMTPFrom == "" {
		return nil, nil
	}
	m, err := email.NewSMTPMailer(email.SMTPParams{
		Host:       smtp.SMTPHost,
		Port:       smtp.SMTPPort,
		Username:   smtp.SMTPUsername,
		Password:   smtp.SMTPPassword,
		From:       smtp.SMTPFrom,
		FromName:   smtp.SMTPFromName,
		UseTLS:     smtp.SMTPUseTLS,
		AppBaseURL: smtp.AppBaseURL,
	})
	if err != nil {
		return nil, err
	}
	return m, nil
}
