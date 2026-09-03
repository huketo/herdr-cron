package cli

import (
	"fmt"
	"io"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

// BuildInfo is the provenance of this binary, as the linker supplied it.
//
// It is a value rather than package state because two things read it — the
// `--version` flag and the daemon heartbeat that `status` reports back
// (docs/spec/04-storage.md §7) — and a test needs to build a root command with
// a known version without mutating a global.
type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

// fallbackVersion is the released version of this source tree. release-please
// rewrites it on every release through the extra-files entry in
// release-please-config.json — do not edit it by hand.
//
// It exists because the Herdr plugin build cannot stamp a version in. Herdr
// runs `[[build]]` commands as argv with no shell, so herdr-plugin.toml cannot
// compute `-X main.version=$(git describe)`, and Herdr clones without tags so
// the toolchain cannot infer one either.
//
// Three files carry this number and release-please moves all three together:
// this constant, the top-level `version` in herdr-plugin.toml, and
// .release-please-manifest.json. A test asserts they agree, because a
// mismatch means an annotation stopped matching and every plugin build now
// reports a version it is not.
const fallbackVersion = "0.2.0" // x-release-please-version

// devVersion is what main.go carries when no linker stamp was applied.
const devVersion = "dev"

// Resolve fills in build metadata the linker did not supply.
//
// Three build paths reach this binary and each knows a different amount:
//
//   - goreleaser stamps -X main.version/commit/date. Most precise; always wins.
//   - `go install ...@v0.1.2` stamps nothing, but the module system records the
//     resolved version in the build info.
//   - The Herdr plugin build runs plain `go build` in a tagless checkout. The
//     toolchain has no version to record, so the version comes from
//     fallbackVersion and the commit from the VCS stamp.
func (b BuildInfo) Resolve() BuildInfo {
	bi, ok := debug.ReadBuildInfo()
	return resolveFrom(b, bi, ok)
}

// resolveFrom is Resolve with the build info handed in, so the three build
// paths can be exercised without three builds. A test binary carries no VCS
// stamps at all, which is why this seam exists.
func resolveFrom(b BuildInfo, bi *debug.BuildInfo, ok bool) BuildInfo {
	stamped := b.Version != "" && b.Version != devVersion

	if !ok || bi == nil {
		if !stamped {
			b.Version = fallbackVersion
		}
		return b
	}

	vcs := vcsStamps(bi)

	if !stamped {
		if isReleaseVersion(bi.Main.Version) {
			// The module system already encodes a dirty tree in this string
			// (e.g. "v0.1.2+dirty"), so do not add a second marker.
			b.Version = strings.TrimPrefix(bi.Main.Version, "v")
		} else {
			b.Version = fallbackVersion
			if vcs.modified {
				b.Version += "-dirty"
			}
		}
	}
	if (b.Commit == "" || b.Commit == "none") && vcs.revision != "" {
		b.Commit = shortRevision(vcs.revision)
	}
	if (b.Date == "" || b.Date == "unknown") && vcs.time != "" {
		b.Date = vcs.time
	}
	return b
}

// pseudoVersion matches the suffix the module system appends when it has to
// synthesise a version: a 14-digit UTC timestamp and a 12-character revision.
//
// The timestamp is introduced by a dash when there is no base tag
// ("v0.0.0-20260901071544-21f4415ac06e") and by a dot when the pseudo-version
// is built on one ("v0.1.2-0.20260901071544-21f4415ac06e"), so both separators
// have to be accepted.
var pseudoVersion = regexp.MustCompile(`[-.]\d{14}-[0-9a-f]{12}$`)

// isReleaseVersion reports whether the module system resolved a version worth
// showing.
//
// Two shapes are not. A binary built from a working tree reports "(devel)". A
// binary built from a checkout with no tags — which is every Herdr plugin
// install, because Herdr clones without them — gets a pseudo-version derived
// from the commit. Neither tells the reader which release they are running,
// and both say less than the VCS stamp reported separately, so
// fallbackVersion is preferred over them.
func isReleaseVersion(v string) bool {
	if v == "" || v == "(devel)" {
		return false
	}
	// A modified tree adds build metadata ("+dirty") after the revision, which
	// would otherwise push the suffix past the anchor.
	base, _, _ := strings.Cut(v, "+")
	return !pseudoVersion.MatchString(base)
}

type vcsInfo struct {
	revision string
	time     string
	modified bool
}

func vcsStamps(bi *debug.BuildInfo) vcsInfo {
	var out vcsInfo
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			out.revision = s.Value
		case "vcs.time":
			out.time = s.Value
		case "vcs.modified":
			out.modified = s.Value == "true"
		}
	}
	return out
}

// shortRevision trims a full commit hash to the usual seven characters.
func shortRevision(rev string) string {
	if len(rev) > 7 {
		return rev[:7]
	}
	return rev
}

// BuildDate parses the recorded date, if it is a timestamp at all.
func (b BuildInfo) BuildDate() (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, b.Date)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// printVersion renders the `--version` line.
//
// It is deliberately plain text rather than an Envelope: `--version` is a flag
// that prints and exits before any command runs, so it has no payload type in
// the table of docs/spec/05-cli.md §2, and `-o` does not apply to it. An agent
// that wants the version as data reads `schema`, which carries it in the
// envelope.
func printVersion(w io.Writer, g *globals) error {
	_, err := fmt.Fprintf(w, "herdr-cron %s (%s, %s) %s %s/%s\n",
		versionOr(g.info.Version), commitOr(g.info.Commit), dateOr(g.info.Date),
		runtime.Version(), runtime.GOOS, runtime.GOARCH)
	return err
}

func commitOr(v string) string {
	if v == "" {
		return "none"
	}
	return v
}

func dateOr(v string) string {
	if v == "" {
		return "unknown"
	}
	return v
}

// versionOr keeps a placeholder out of user-visible output when a caller
// constructed a zero BuildInfo — every test does, and so does `go run` with no
// module context.
func versionOr(v string) string {
	if v == "" {
		return devVersion
	}
	return v
}
