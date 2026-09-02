package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// manifestVersion pulls the top-level `version` out of herdr-plugin.toml
// without pulling in a TOML parser for one line.
var manifestVersion = regexp.MustCompile(`(?m)^version\s*=\s*"([^"]+)"`)

// semver is deliberately strict: Herdr parses this field, and release-please
// writes it, so anything else means one of them is about to be surprised.
var semver = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// TestPluginManifestVersionTracksTheReleaseManifest guards a silent failure.
//
// release-please owns both `.release-please-manifest.json` and the `version`
// in `herdr-plugin.toml`, the latter through an `extra-files` entry with a
// jsonpath. If that jsonpath ever stops matching — the key is renamed, moved
// under a table, or the entry is dropped — release-please does not complain.
// It updates the JSON, skips the TOML, and every subsequent release ships a
// plugin that reports a version it is not. Herdr shows that version to the
// user and `plugin install` records it.
//
// Comparing the two files is the only check that notices.
func TestPluginManifestVersionTracksTheReleaseManifest(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	want := releasedVersion(t)

	pluginTOML, err := os.ReadFile(filepath.Join(root, "herdr-plugin.toml"))
	if err != nil {
		t.Skipf("plugin manifest unavailable: %v", err)
	}
	m := manifestVersion.FindSubmatch(pluginTOML)
	if m == nil {
		t.Fatal("herdr-plugin.toml has no top-level `version = \"...\"`; " +
			"the release-please extra-files jsonpath $.version cannot match it")
	}
	got := string(m[1])

	if got != want {
		t.Errorf("herdr-plugin.toml version = %q, release manifest says %q; "+
			"release-please should keep these equal via the extra-files entry in release-please-config.json",
			got, want)
	}
	if !semver.MatchString(got) {
		t.Errorf("herdr-plugin.toml version = %q, want a bare MAJOR.MINOR.PATCH", got)
	}
}

// TestFallbackVersionTracksTheReleaseManifest is the buildinfo half of the same
// invariant: release-please rewrites the annotated constant, and a mismatch
// means the annotation stopped matching and every Herdr plugin build now
// reports a stale version.
func TestFallbackVersionTracksTheReleaseManifest(t *testing.T) {
	t.Parallel()

	if want := releasedVersion(t); fallbackVersion != want {
		t.Errorf("fallbackVersion = %q, release manifest says %q; "+
			"the x-release-please-version annotation in buildinfo.go is not being applied",
			fallbackVersion, want)
	}
}

// TestReleaseConfigUpdatesBothVersionSites asserts the wiring the two tests
// above depend on actually exists.
func TestReleaseConfigUpdatesBothVersionSites(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "release-please-config.json"))
	if err != nil {
		t.Skipf("release config unavailable: %v", err)
	}

	var cfg struct {
		Packages map[string]struct {
			ExtraFiles []struct {
				Type     string `json:"type"`
				Path     string `json:"path"`
				JSONPath string `json:"jsonpath"`
			} `json:"extra-files"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parse release-please-config.json: %v", err)
	}

	pkg, ok := cfg.Packages["."]
	if !ok {
		t.Fatal(`release-please-config.json has no "." package`)
	}
	want := map[string]string{
		// The manifest version Herdr shows the user.
		"herdr-plugin.toml": "toml",
		// The version a build without a linker stamp falls back to, which is
		// every Herdr plugin build.
		"internal/cli/buildinfo.go": "generic",
	}
	for _, f := range pkg.ExtraFiles {
		kind, tracked := want[f.Path]
		if !tracked {
			continue
		}
		if f.Type != kind {
			t.Errorf("extra-files entry for %s has type %q, want %q", f.Path, f.Type, kind)
		}
		if f.Path == "herdr-plugin.toml" && f.JSONPath != "$.version" {
			t.Errorf("extra-files entry for %s has jsonpath %q, want \"$.version\"", f.Path, f.JSONPath)
		}
		delete(want, f.Path)
	}
	for path := range want {
		t.Errorf("release-please-config.json does not list %s in extra-files, "+
			"so its version will stop tracking releases", path)
	}
}

// TestPluginManifestUsesTheSchemaHerdrAccepts keeps herdr-plugin.toml honest.
//
// Two failures this catches, both of which the Go build is blind to:
//
// Herdr 0.8.2 requires `command` to be an argv **array** and has no `args`
// key at all. A manifest written with `command = "go"` + `args = [...]` is
// valid TOML and parses fine in a test that only checks TOML syntax — but
// `herdr plugin link` rejects the whole file with
// `plugin_manifest_parse_failed`, so the plugin cannot be installed at all.
// That is exactly how this repo's first manifest shipped.
//
// And Herdr execs the paths named by `[[startup]]`, `[[actions]]` and
// `[[panes]]` directly, with no shell and no PATH lookup for a relative path.
// A section pointing somewhere `[[build]]` never writes is a plugin that
// installs and then fails at first use.
func TestPluginManifestUsesTheSchemaHerdrAccepts(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "herdr-plugin.toml"))
	if err != nil {
		t.Skipf("plugin manifest unavailable: %v", err)
	}
	text := string(raw)

	if argsKey.MatchString(text) {
		t.Error("herdr-plugin.toml has an `args = [...]` key; Herdr has no such key — " +
			"the whole argv goes in `command`, and a manifest with `args` fails to parse")
	}

	commands := commandArray.FindAllStringSubmatch(text, -1)
	if len(commands) == 0 {
		t.Fatal("herdr-plugin.toml declares no `command = [...]`; Herdr requires an argv array, not a string")
	}

	// The one path the build produces, and therefore the only one any other
	// section may exec.
	const output = "bin/herdr-cron"
	sawBuild := false
	for _, m := range commands {
		argv := tomlStrings(m[1])
		if len(argv) == 0 {
			t.Errorf("herdr-plugin.toml has an empty `command = []`")
			continue
		}
		joined := strings.Join(argv, " ")
		switch argv[0] {
		case "go":
			// The build step. It must write where everything else reads.
			sawBuild = true
			if !strings.Contains(joined, "-o "+output) {
				t.Errorf("[[build]] runs %q but does not write %s", joined, output)
			}
		case output:
			// A startup hook or an action, exec'd relative to the plugin root.
		case "sh":
			// The pane, which needs $HERDR_PLUGIN_ROOT because a pane's cwd is
			// the workspace under work, not the plugin root.
			if !strings.Contains(joined, "$HERDR_PLUGIN_ROOT/"+output) {
				t.Errorf("a `sh -c` command runs %q, which does not exec %s under the plugin root",
					joined, output)
			}
		default:
			t.Errorf("herdr-plugin.toml runs %q, which the [[build]] step does not produce", joined)
		}
	}
	if !sawBuild {
		t.Errorf("herdr-plugin.toml has no [[build]] command producing %s", output)
	}
}

// argsKey matches the key that does not exist in Herdr's manifest schema.
var argsKey = regexp.MustCompile(`(?m)^args\s*=`)

// commandArray captures the body of a `command = [ ... ]` array.
var commandArray = regexp.MustCompile(`(?m)^command\s*=\s*\[([^\]]*)\]`)

// tomlStrings pulls the quoted elements out of a single-line TOML array,
// honouring the one escape that appears here (`\"`).
var tomlStrings = func(body string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`).FindAllStringSubmatch(body, -1) {
		out = append(out, strings.ReplaceAll(m[1], `\"`, `"`))
	}
	return out
}

// releasedVersion reads the single version release-please owns.
func releasedVersion(t *testing.T) string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(repoRoot(t), ".release-please-manifest.json"))
	if err != nil {
		t.Skipf("release manifest unavailable: %v", err)
	}
	var released map[string]string
	if err := json.Unmarshal(raw, &released); err != nil {
		t.Fatalf("parse .release-please-manifest.json: %v", err)
	}
	v, ok := released["."]
	if !ok {
		t.Fatal(`.release-please-manifest.json has no "." entry`)
	}
	return v
}

// repoRoot walks up from the package directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for range 8 {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("could not find the module root from " + strings.TrimSpace(dir))
	return ""
}
