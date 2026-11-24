package adminauth

import "strings"

// SummarizeUserAgent turns a User-Agent header into the short label the
// sessions list shows ("Firefox on macOS"). It recognizes the common browsers
// and platforms only; anything else falls back to the first product token
// ("curl"), and an empty header to "".
func SummarizeUserAgent(ua string) string {
	ua = strings.TrimSpace(ua)
	if ua == "" {
		return ""
	}
	browser := userAgentBrowser(ua)
	platform := userAgentPlatform(ua)
	switch {
	case browser != "" && platform != "":
		return browser + " on " + platform
	case browser != "":
		return browser
	case platform != "":
		return platform
	}
	product, _, _ := strings.Cut(ua, " ")
	product, _, _ = strings.Cut(product, "/")
	if len(product) > 40 {
		product = product[:40]
	}
	return product
}

// userAgentBrowser checks the most specific tokens first: Edge and Opera
// also claim Chrome, and Chrome also claims Safari.
func userAgentBrowser(ua string) string {
	switch {
	case strings.Contains(ua, "Edg/") || strings.Contains(ua, "EdgA/") || strings.Contains(ua, "EdgiOS/"):
		return "Edge"
	case strings.Contains(ua, "OPR/") || strings.Contains(ua, "Opera"):
		return "Opera"
	case strings.Contains(ua, "Firefox/") || strings.Contains(ua, "FxiOS/"):
		return "Firefox"
	case strings.Contains(ua, "Chrome/") || strings.Contains(ua, "CriOS/") || strings.Contains(ua, "Chromium/"):
		return "Chrome"
	case strings.Contains(ua, "Safari/") && strings.Contains(ua, "Version/"):
		return "Safari"
	}
	return ""
}

func userAgentPlatform(ua string) string {
	switch {
	case strings.Contains(ua, "Windows"):
		return "Windows"
	case strings.Contains(ua, "Android"):
		return "Android"
	case strings.Contains(ua, "iPhone") || strings.Contains(ua, "iPod"):
		return "iOS"
	case strings.Contains(ua, "iPad"):
		return "iPadOS"
	case strings.Contains(ua, "Mac OS X") || strings.Contains(ua, "Macintosh"):
		return "macOS"
	case strings.Contains(ua, "CrOS"):
		return "ChromeOS"
	case strings.Contains(ua, "Linux"):
		return "Linux"
	}
	return ""
}
