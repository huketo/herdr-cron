package herdr

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// TrustVerdict is three-valued: "no checker for this kind" is not an error
// (docs/spec/07-herdr-integration.md §5.3).
type TrustVerdict int

const (
	// TrustUnknown means no checker exists for this agent kind, so the
	// pre-flight neither passed nor failed. Only `claude` has a checker; any
	// other kind proceeds and may stall, which is the risk §5.3 records.
	TrustUnknown TrustVerdict = iota
	// TrustTrusted means the agent has been approved for this cwd before, so
	// it will not open an approval dialog nobody is there to answer.
	TrustTrusted
	// TrustUntrusted ends the run as blocked / cwd_not_trusted before anything
	// is created — no server started, no pane opened, no money spent.
	TrustUntrusted
)

func (v TrustVerdict) String() string {
	switch v {
	case TrustTrusted:
		return "trusted"
	case TrustUntrusted:
		return "untrusted"
	default:
		return "unknown"
	}
}

// CheckTrust reports whether an agent kind has been trusted for a directory.
//
// This exists because of a verified failure: `agent start` in an untrusted cwd returns
// agent_not_ready and parks the agent on a safety dialog forever with nobody to answer it
// (docs/spec/07-herdr-integration.md §5.1).
func CheckTrust(agentKind, cwd string) (TrustVerdict, error) {
	if cwd == "" {
		return TrustUnknown, nil
	}
	switch agentKind {
	case "claude":
		return claudeTrust(cwd)
	default:
		// No verified mechanism for the other 21 kinds. Blocking on an unimplementable
		// check would make them all unusable; the protection is instead the
		// agent_not_ready -> blocked classification, one wasted `agent start` later.
		return TrustUnknown, nil
	}
}

type claudeConfig struct {
	Projects map[string]struct {
		HasTrustDialogAccepted bool `json:"hasTrustDialogAccepted"`
	} `json:"projects"`
}

func claudeTrust(cwd string) (TrustVerdict, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return TrustUnknown, err
	}
	b, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		if os.IsNotExist(err) {
			// Claude Code has never run here; every directory is untrusted.
			return TrustUntrusted, nil
		}
		return TrustUnknown, err
	}
	var cfg claudeConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return TrustUnknown, fmt.Errorf("~/.claude.json is not readable JSON: %w", err)
	}
	want := normalisePath(cwd)
	for k, v := range cfg.Projects {
		if normalisePath(k) == want {
			if v.HasTrustDialogAccepted {
				return TrustTrusted, nil
			}
			return TrustUntrusted, nil
		}
	}
	return TrustUntrusted, nil
}

// normalisePath compares cleaned absolute paths, resolving symlinks, and case-insensitively
// on Windows and macOS.
func normalisePath(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = resolved
	}
	p = filepath.Clean(p)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.ToLower(p)
	}
	return p
}

// TrustRemediation is the text a human needs to fix an untrusted directory once
// (docs/spec/07-herdr-integration.md §5.5).
func TrustRemediation(jobID, agentKind, cwd string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "herdr-cron: job %q cannot run unattended.\n", jobID)
	fmt.Fprintf(&b, "  The agent kind %q has never been trusted for this directory:\n", agentKind)
	fmt.Fprintf(&b, "    %s\n", cwd)
	b.WriteString("  A scheduled `agent start` there returns agent_not_ready and parks the agent on\n")
	b.WriteString("  a safety dialog no scheduler can answer.\n\n")
	b.WriteString("  Fix it once, interactively:\n")
	fmt.Fprintf(&b, "    cd %s && %s\n", cwd, agentKind)
	b.WriteString("    answer \"Yes, I trust this folder\", then exit the agent.\n\n")
	b.WriteString("  Verify:  herdr-cron validate\n")
	return b.String()
}
