package statuspage

import (
	"net/http"
	"strings"

	"github.com/yassinebenameur/probara/shared/securityheaders"
	"github.com/yassinebenameur/probara/shared/statustemplate"
)

// pageCSP is a page's Content-Security-Policy minus the nonce: the effective
// csp_mode and the frame-ancestors list. It is cached with the rendered HTML;
// apply mints the nonce per response.
type pageCSP struct {
	mode           string
	frameAncestors []string
}

// pageCSPFor resolves a page's policy inputs. customTemplate reports that
// the render uses tenant-authored template source (published or draft),
// which, like custom head/footer HTML, makes the automatic mode compatible.
func pageCSPFor(data *StatusPageData, appBaseURL string, customTemplate bool) pageCSP {
	customized := customTemplate ||
		strings.TrimSpace(data.CustomHeadHTML) != "" ||
		strings.TrimSpace(data.CustomFooterHTML) != ""
	return pageCSP{
		mode:           securityheaders.ResolveCSPMode(data.CSPMode, customized),
		frameAncestors: securityheaders.FrameAncestors(appBaseURL, data.AllowedEmbedOrigins),
	}
}

// apply sets the CSP header for one response and returns html with the
// template's nonce placeholder replaced by that response's nonce. Call it
// only for a full 200 response: a 304 must carry no CSP, so the browser
// keeps the stored header whose nonce matches its stored body.
func (p pageCSP) apply(h http.Header, html string) string {
	nonce := securityheaders.NewNonce()
	h.Set(securityheaders.HeaderCSP, securityheaders.StatusPagePolicy(p.mode, nonce, p.frameAncestors))
	return strings.ReplaceAll(html, statustemplate.NoncePlaceholder, nonce)
}
