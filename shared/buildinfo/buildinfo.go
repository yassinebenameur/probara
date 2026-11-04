// Package buildinfo carries the release version a binary was built at.
//
// Version is stamped at link time; every Go Dockerfile does it from its
// VERSION build-arg, which docker-build.yml fills with the metadata action's
// version (the release tag without its "v" on tag builds, the branch name on
// branch builds):
//
//	go build -ldflags "-X github.com/yassinebenameur/probara/shared/buildinfo.Version=2.0.0" ...
//
// Unstamped builds (go run, go test, local binaries) report "dev".
package buildinfo

import (
	"regexp"
	"strings"
)

// Version is the release version, or "dev" when the build was not stamped.
// A var, not a const, so -ldflags -X can set it.
var Version = "dev"

// releaseRE is a semver release or prerelease, optionally "v"-prefixed. Build
// metadata (+...) is excluded on purpose: it is not a valid image tag.
var releaseRE = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z][0-9A-Za-z.-]*)?$`)

// ReleaseVersion returns Version without a leading "v" and true when it names
// a release (stable or prerelease). docker-build pushes every image under that
// exact tag, so it is safe to reference as an image tag. Branch and local
// builds ("dev", "main", ...) return false.
func ReleaseVersion() (string, bool) {
	v := strings.TrimSpace(Version)
	if !releaseRE.MatchString(v) {
		return "", false
	}
	return strings.TrimPrefix(v, "v"), true
}
