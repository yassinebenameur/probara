package plugin

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Send errors are classified so the async consumer can react correctly:
//
//   - Permanent: retrying cannot help (bad credentials, deleted webhook,
//     rejected payload). The consumer stops redelivering and logs it.
//   - RetryAfter: the provider asked for a specific delay (HTTP 429 with a
//     Retry-After header). The consumer redelivers after that delay.
//   - anything else: transient, redelivered on the default backoff schedule.

type permanentError struct{ err error }

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

// Permanent marks err as not worth retrying. Nil stays nil.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &permanentError{err: err}
}

// IsPermanent reports whether err (or anything it wraps) was marked Permanent.
func IsPermanent(err error) bool {
	var p *permanentError
	return errors.As(err, &p)
}

type retryAfterError struct {
	err   error
	after time.Duration
}

func (e *retryAfterError) Error() string { return e.err.Error() }
func (e *retryAfterError) Unwrap() error { return e.err }

// RetryAfter marks err as transient with a provider-requested delay.
func RetryAfter(err error, after time.Duration) error {
	if err == nil {
		return nil
	}
	return &retryAfterError{err: err, after: after}
}

// RetryAfterDelay returns the provider-requested delay when err carries one.
func RetryAfterDelay(err error) (time.Duration, bool) {
	var r *retryAfterError
	if errors.As(err, &r) {
		return r.after, true
	}
	return 0, false
}

// maxRetryAfter caps a provider's Retry-After so one hostile or confused
// endpoint cannot park a notification for hours.
const maxRetryAfter = 10 * time.Minute

// CheckResponse turns a provider HTTP response into nil (2xx) or a classified
// error. service names the provider in the message ("slack webhook"). A short,
// single-line excerpt of the response body is included because providers put
// the actionable reason there ("invalid routing key", "chat not found").
// The caller still owns resp.Body.
func CheckResponse(resp *http.Response, service string) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	err := fmt.Errorf("%s returned status %d%s", service, resp.StatusCode, bodyExcerpt(resp.Body))
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		if d, ok := parseRetryAfter(resp.Header.Get("Retry-After")); ok {
			return RetryAfter(err, d)
		}
		return err
	case resp.StatusCode == http.StatusRequestTimeout, resp.StatusCode == http.StatusTooEarly:
		return err
	case resp.StatusCode >= 500:
		if d, ok := parseRetryAfter(resp.Header.Get("Retry-After")); ok {
			return RetryAfter(err, d)
		}
		return err
	default:
		// 3xx (redirects are refused, see NewHTTPClient) and the remaining
		// 4xx describe the channel config, not a passing condition.
		return Permanent(err)
	}
}

func bodyExcerpt(body io.Reader) string {
	if body == nil {
		return ""
	}
	b, _ := io.ReadAll(io.LimitReader(body, 512))
	s := strings.Join(strings.Fields(string(b)), " ")
	if s == "" {
		return ""
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return ": " + s
}

func parseRetryAfter(v string) (time.Duration, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	var d time.Duration
	if secs, err := strconv.Atoi(v); err == nil {
		d = time.Duration(secs) * time.Second
	} else if t, err := http.ParseTime(v); err == nil {
		d = time.Until(t)
	} else {
		return 0, false
	}
	if d < time.Second {
		d = time.Second
	}
	if d > maxRetryAfter {
		d = maxRetryAfter
	}
	return d, true
}
