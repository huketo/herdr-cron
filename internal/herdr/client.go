// Package herdr is the only package in herdr-cron that knows the `herdr` CLI exists.
//
// Every call shells out to the binary rather than speaking its socket: Herdr's own docs
// push plugins toward the CLI precisely to avoid owning the Unix-socket vs Windows
// named-pipe split (docs/spec/07-herdr-integration.md §1.1).
package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// MinVersion is the floor: 0.8.2 is the version the headless path was probed against, and
// pinning to the probed version is the only honest floor
// (docs/spec/07-herdr-integration.md §1.3).
const MinVersion = "0.8.2"

// Errors the caller branches on. Herdr's error codes are open-ended, so classification
// falls through to ErrUnexpected (docs/spec/07-herdr-integration.md §4).
var (
	ErrUnavailable        = errors.New("herdr_unavailable")
	ErrVersionUnsupported = errors.New("herdr_version_unsupported")
	ErrUnexpected         = errors.New("herdr_unexpected")
	// ErrSyntax means herdr-cron built a bad command line: a bug here, never a job failure.
	ErrSyntax = errors.New("internal: herdr rejected the command line")
)

// ErrorBody is herdr's error payload. It carries no code enum.
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *ErrorBody) Error() string { return e.Code + ": " + e.Message }

// Envelope is herdr's universal CLI response: id, then exactly one of result or error.
type Envelope struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *ErrorBody      `json:"error"`
}

// Client talks to one herdr session.
type Client struct {
	bin     string
	session string // empty means the default session
}

var (
	resolveOnce sync.Once
	resolvedBin string
	resolveErr  error
)

// Resolve finds the herdr binary once per process (docs/spec/07-herdr-integration.md §1.2).
func Resolve() (string, error) {
	resolveOnce.Do(func() {
		if v := os.Getenv("HERDR_BIN_PATH"); v != "" && executable(v) {
			resolvedBin = v
			return
		}
		if p, err := exec.LookPath(binName()); err == nil {
			resolvedBin = p
			return
		}
		// Herdr runs plugin commands with a minimal PATH, so LookPath can fail on a
		// machine where herdr is plainly installed.
		for _, cand := range fallbackPaths() {
			if executable(cand) {
				resolvedBin = cand
				return
			}
		}
		resolveErr = fmt.Errorf("%w: no herdr binary on PATH, in HERDR_BIN_PATH, or at a known install location",
			ErrUnavailable)
	})
	return resolvedBin, resolveErr
}

func binName() string {
	if runtime.GOOS == "windows" {
		return "herdr.exe"
	}
	return "herdr"
}

func fallbackPaths() []string {
	var out []string
	if root := os.Getenv("HERDR_PLUGIN_ROOT"); root != "" {
		out = append(out, filepath.Join(root, "bin", binName()))
	}
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "windows" {
		if lad := os.Getenv("LocalAppData"); lad != "" {
			out = append(out, filepath.Join(lad, "Programs", "herdr", "herdr.exe"))
		}
		return out
	}
	if home != "" {
		out = append(out, filepath.Join(home, ".local", "bin", "herdr"))
	}
	return append(out, "/opt/homebrew/bin/herdr", "/usr/local/bin/herdr")
}

func executable(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0
}

// New builds a client for a session name. Pass "" for the default session.
func New(session string) (*Client, error) {
	bin, err := Resolve()
	if err != nil {
		return nil, err
	}
	return &Client{bin: bin, session: session}, nil
}

// Session reports the session this client targets, "" meaning the default.
func (c *Client) Session() string { return c.session }

// CheckVersion enforces the floor. The standalone CLI has no plugin link step, so this is
// the only gate outside plugin mode (docs/spec/07-herdr-integration.md §1.3).
func (c *Client) CheckVersion(ctx context.Context) error {
	out, err := exec.CommandContext(ctx, c.bin, "--version").Output()
	if err != nil {
		return fmt.Errorf("%w: cannot run %s --version: %w", ErrUnavailable, c.bin, err)
	}
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) < 2 {
		return fmt.Errorf("%w: unreadable version %q", ErrUnavailable, string(out))
	}
	if compareVersions(fields[1], MinVersion) < 0 {
		return fmt.Errorf("%w: herdr %s is below the %s floor", ErrVersionUnsupported, fields[1], MinVersion)
	}
	return nil
}

// compareVersions orders dotted numeric versions, ignoring any pre-release suffix.
func compareVersions(a, b string) int {
	as, bs := strings.Split(cutSuffix(a), "."), strings.Split(cutSuffix(b), ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		x, y := atoi(get(as, i)), atoi(get(bs, i))
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func cutSuffix(v string) string {
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		return v[:i]
	}
	return v
}

func get(v []string, i int) string {
	if i < len(v) {
		return v[i]
	}
	return "0"
}

func atoi(v string) int {
	n, _ := strconv.Atoi(v)
	return n
}

// argv builds a command line. --session is global and MUST precede the subcommand.
func (c *Client) argv(args ...string) []string {
	if c.session == "" {
		return args
	}
	return append([]string{"--session", c.session}, args...)
}

// Raw runs a command and returns stdout, without envelope decoding.
func (c *Client) Raw(ctx context.Context, args ...string) ([]byte, []byte, int, error) {
	cmd := exec.CommandContext(ctx, c.bin, c.argv(args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
		err = nil
	}
	return stdout.Bytes(), stderr.Bytes(), code, err
}

// call runs a command, decodes the envelope, and unmarshals result into out.
//
// Server errors are JSON on stderr with exit 1; a syntax error exits 2 and is a
// herdr-cron bug (docs/spec/07-herdr-integration.md §1.4).
func (c *Client) call(ctx context.Context, out any, args ...string) error {
	stdout, stderr, code, err := c.Raw(ctx, args...)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if code == 2 {
		return fmt.Errorf("%w: herdr %s: %s", ErrSyntax, strings.Join(args, " "),
			strings.TrimSpace(string(stderr)))
	}

	// Prefer whichever stream parses: errors land on stderr, results on stdout.
	for _, buf := range [][]byte{stdout, stderr} {
		if len(bytes.TrimSpace(buf)) == 0 {
			continue
		}
		var env Envelope
		if err := json.Unmarshal(bytes.TrimSpace(buf), &env); err != nil {
			continue
		}
		if env.Error != nil {
			return env.Error
		}
		if out == nil || len(env.Result) == 0 {
			return nil
		}
		return json.Unmarshal(env.Result, out)
	}
	if code != 0 {
		return fmt.Errorf("%w: herdr %s exited %d: %s", ErrUnexpected,
			strings.Join(args, " "), code, strings.TrimSpace(string(stderr)))
	}
	return fmt.Errorf("%w: herdr %s produced no parseable envelope", ErrUnexpected, strings.Join(args, " "))
}

// Code returns herdr's error code for an error, or "" when it is not one.
func Code(err error) string {
	var eb *ErrorBody
	if errors.As(err, &eb) {
		return eb.Code
	}
	return ""
}

// ---------------------------------------------------------------- data types

// Agent mirrors `.result.agent`. Absent `name` means "no alias", never an empty alias.
type Agent struct {
	Agent            string `json:"agent"`
	AgentStatus      string `json:"agent_status"`
	Cwd              string `json:"cwd"`
	ForegroundCwd    string `json:"foreground_cwd"`
	InteractiveReady bool   `json:"interactive_ready"`
	Name             string `json:"name"`
	PaneID           string `json:"pane_id"`
	StateChangeSeq   int    `json:"state_change_seq"`
	TabID            string `json:"tab_id"`
	TerminalTitle    string `json:"terminal_title"`
	WorkspaceID      string `json:"workspace_id"`
}

// Agent statuses, the enum Herdr uses everywhere.
const (
	StatusIdle    = "idle"
	StatusWorking = "working"
	StatusBlocked = "blocked"
	StatusDone    = "done"
	StatusUnknown = "unknown"
)

type agentEnvelope struct {
	Type  string   `json:"type"`
	Agent Agent    `json:"agent"`
	Argv  []string `json:"argv"`
}

type agentListEnvelope struct {
	Type   string  `json:"type"`
	Agents []Agent `json:"agents"`
}

// Explain is the bare, unenveloped object `agent explain --json` returns.
type Explain struct {
	Agent          string `json:"agent"`
	FallbackReason string `json:"fallback_reason"`
	MatchedRule    string `json:"matched_rule"`
	State          string `json:"state"`
	VisibleBlocker bool   `json:"visible_blocker"`
	VisibleIdle    bool   `json:"visible_idle"`
	VisibleWorking bool   `json:"visible_working"`
	Warning        string `json:"warning"`

	// Raw is the whole document, because the fallback label must also be matched
	// literally anywhere in the output (docs/spec/07-herdr-integration.md §4.1).
	Raw string `json:"-"`
}

// IdleFallback is the label meaning Herdr could not classify the screen and defaulted to
// idle — which is how a stalled approval dialog looks like a finished run.
const IdleFallback = "default_known_agent_idle_fallback"

// ------------------------------------------------------------------ commands

// StartAgentRequest starts a supported agent in an existing shell pane.
type StartAgentRequest struct {
	Name      string
	Kind      string
	PaneID    string
	TimeoutMS int
}

// StartAgent launches a coding agent in an existing pane and waits for it to
// report interactive readiness. Verified at ~4 s against herdr 0.8.2 on a
// headless server with no client ever attached
// (docs/spec/07-herdr-integration.md §3 step 6).
//
// The timeout is clamped rather than validated: the socket schema requires
// greater than 3000 ms and at most 300000, and a job whose configured timeout
// falls outside that would otherwise fail as a herdr-cron bug rather than run.
func (c *Client) StartAgent(ctx context.Context, req StartAgentRequest) (Agent, error) {
	timeout := req.TimeoutMS
	// The socket schema requires greater than 3000 and at most 300000.
	if timeout <= 3000 {
		timeout = 90000
	}
	if timeout > 300000 {
		timeout = 300000
	}
	var out agentEnvelope
	err := c.call(ctx, &out, "agent", "start", req.Name,
		"--kind", req.Kind, "--pane", req.PaneID, "--timeout", strconv.Itoa(timeout))
	return out.Agent, err
}

// Prompt submits the prompt and starts the wait in one request, avoiding a race between
// separate calls. --until MUST NOT be passed: the default match set is exactly
// herdr-cron's terminal set, and it excludes `unknown` on purpose
// (docs/spec/07-herdr-integration.md §3 step 7).
func (c *Client) Prompt(ctx context.Context, target, text string, timeoutMS int) (Agent, error) {
	if timeoutMS <= 0 {
		timeoutMS = 300000
	}
	var out agentEnvelope
	err := c.call(ctx, &out, "agent", "prompt", target, text,
		"--wait", "--timeout", strconv.Itoa(timeoutMS))
	return out.Agent, err
}

// ReadSource selects which snapshot to read.
type ReadSource string

const (
	// SourceRecentUnwrapped is the transcript as the agent produced it, with
	// terminal wrapping undone. It is what a run's captured output is built
	// from, because a wrapped snapshot breaks the column-0 assistant-marker
	// match (docs/spec/07-herdr-integration.md §4.2).
	SourceRecentUnwrapped ReadSource = "recent-unwrapped"
	// SourceDetection is the narrower window Herdr's own status detection
	// looks at. Used for diagnosing a stalled run, never for the transcript.
	SourceDetection ReadSource = "detection"
)

// Read returns a terminal snapshot.
//
// Verified against herdr 0.8.2: `agent read` prints **raw terminal text** on stdout, not
// an envelope — `herdr agent read --help` offers `--format text|ansi` and no `--json`.
// An error still arrives as a JSON envelope on stderr with exit 1.
func (c *Client) Read(ctx context.Context, target string, src ReadSource, lines int) (string, error) {
	args := []string{"agent", "read", target, "--source", string(src)}
	if lines > 0 {
		args = append(args, "--lines", strconv.Itoa(lines))
	}
	stdout, stderr, code, err := c.Raw(ctx, args...)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if code == 2 {
		return "", fmt.Errorf("%w: herdr %s: %s", ErrSyntax, strings.Join(args, " "),
			strings.TrimSpace(string(stderr)))
	}
	if code != 0 {
		var env Envelope
		if json.Unmarshal(bytes.TrimSpace(stderr), &env) == nil && env.Error != nil {
			return "", env.Error
		}
		return "", fmt.Errorf("%w: agent read exited %d: %s", ErrUnexpected, code,
			strings.TrimSpace(string(stderr)))
	}
	return string(stdout), nil
}

// Explain is not enveloped: it is a bare object, so it needs its own decode path.
func (c *Client) Explain(ctx context.Context, target string) (Explain, error) {
	stdout, stderr, code, err := c.Raw(ctx, "agent", "explain", target, "--json")
	if err != nil {
		return Explain{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	body := bytes.TrimSpace(stdout)
	if len(body) == 0 {
		body = bytes.TrimSpace(stderr)
	}
	var ex Explain
	if err := json.Unmarshal(body, &ex); err != nil {
		return Explain{}, fmt.Errorf("%w: agent explain exited %d: %s", ErrUnexpected, code, string(body))
	}
	ex.Raw = string(body)
	return ex, nil
}

// AgentList reports every agent the session's server knows about. Used to
// resolve a run's agent by name after a restart, since herdr-cron names its
// agents deterministically per job.
func (c *Client) AgentList(ctx context.Context) ([]Agent, error) {
	var out agentListEnvelope
	err := c.call(ctx, &out, "agent", "list")
	return out.Agents, err
}

// ClosePane tears down a run's pane. It is idempotent: a pane that is already
// gone is success, because cleanup runs on every terminal outcome including the
// ones where the pane died on its own.
func (c *Client) ClosePane(ctx context.Context, paneID string) error {
	err := c.call(ctx, nil, "pane", "close", paneID)
	// "The pane is gone" is a benign race that converges.
	if code := Code(err); code == "pane_not_found" {
		return nil
	}
	return err
}
