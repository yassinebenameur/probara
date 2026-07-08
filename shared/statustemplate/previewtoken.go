package statustemplate

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Preview tokens gate the draft-template preview route on the public
// status-page service. The admin API mints one per editor session; both
// services share the secret via the STATUS_PAGE_PREVIEW_SECRET env var.
// When no secret is configured the status-page service serves draft previews
// unauthenticated (dev-friendly, and drafts only ever render data that is
// already public on the live page).
//
// Token format: "<expiresUnix>.<hex hmac-sha256(secret, pageID+"."+expiresUnix)>".

// MintPreviewToken returns a draft-preview token for pageID valid until
// expiresAt.
func MintPreviewToken(secret, pageID string, expiresAt time.Time) string {
	exp := strconv.FormatInt(expiresAt.Unix(), 10)
	return exp + "." + previewSignature(secret, pageID, exp)
}

// VerifyPreviewToken reports whether token grants draft-preview access to
// pageID at time now.
func VerifyPreviewToken(secret, pageID, token string, now time.Time) bool {
	exp, sig, ok := strings.Cut(token, ".")
	if !ok {
		return false
	}
	expUnix, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || now.Unix() > expUnix {
		return false
	}
	expected := previewSignature(secret, pageID, exp)
	return hmac.Equal([]byte(sig), []byte(expected))
}

func previewSignature(secret, pageID, exp string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%s.%s", pageID, exp)
	return hex.EncodeToString(mac.Sum(nil))
}
