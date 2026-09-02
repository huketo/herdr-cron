package herdr

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// DefaultSession is the dedicated session agent jobs run in (decision D7). Scheduled runs
// create and destroy panes; doing that in the human's default session means a 03:00 job
// reshuffling the workspace list they left open.
const DefaultSession = "herdr-cron"

// HostWorkspaceLabel marks the workspace herdr-cron owns. It never adopts one it did not
// create (docs/spec/07-herdr-integration.md §2.4).
const HostWorkspaceLabel = "herdr-cron"

const (
	serverStartTimeout = 15 * time.Second
	serverPollInterval = 250 * time.Millisecond
)

// Status is `herdr status --json`.
type Status struct {
	Client struct {
		Version string `json:"version"`
		Session string `json:"session"`
	} `json:"client"`
	Server struct {
		Status     string `json:"status"` // running | not_running
		Running    bool   `json:"running"`
		Version    string `json:"version"`
		Compatible *bool  `json:"compatible"`
		Socket     string `json:"socket"`
	} `json:"server"`
}

// Status probes the session. It exits 0 with the server down and does not create the
// session, which is what makes it the correct pre-flight
// (docs/spec/07-herdr-integration.md §2.2).
func (c *Client) Status(ctx context.Context) (Status, error) {
	stdout, _, _, err := c.Raw(ctx, "status", "--json")
	if err != nil {
		return Status{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	var s Status
	if err := json.Unmarshal(stdout, &s); err != nil {
		return Status{}, fmt.Errorf("%w: unreadable status output", ErrUnexpected)
	}
	return s, nil
}

// EnsureServer starts a headless server for the session when none is running.
//
// The child is detached with piped stdio and never waited on: what bare `herdr server`
// does with a controlling terminal is unverified, and this is the only shape with
// evidence behind it (docs/spec/07-herdr-integration.md §2.3).
func (c *Client) EnsureServer(ctx context.Context) error {
	st, err := c.Status(ctx)
	if err != nil {
		return err
	}
	if st.Server.Status == "running" {
		if st.Server.Compatible != nil && !*st.Server.Compatible {
			return fmt.Errorf("%w: the running server reports itself incompatible with this client",
				ErrVersionUnsupported)
		}
		return nil
	}

	// Deliberately not CommandContext: the server outlives this run by design —
	// the next scheduled job reuses it, and a Herdr session survives
	// detachment. Binding it to ctx would tear the session down as soon as the
	// run that started it finished. Readiness is polled below instead, and ctx
	// still bounds that wait.
	//nolint:noctx // the headless server must survive this run
	cmd := exec.Command(c.bin, c.argv("server")...)
	cmd.Stdin = nil
	cmd.Stdout = nil // piped, never inherited: no controlling terminal
	cmd.Stderr = nil
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%w: cannot start a server for session %q: %w", ErrUnavailable, c.session, err)
	}
	_ = cmd.Process.Release()

	deadline := time.Now().Add(serverStartTimeout)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		time.Sleep(serverPollInterval)
		st, err := c.Status(ctx)
		if err == nil && st.Server.Status == "running" {
			return nil
		}
	}
	return fmt.Errorf("%w: the server for session %q did not come up within %s",
		ErrUnavailable, c.session, serverStartTimeout)
}

// ------------------------------------------------------------------ topology

// Workspace is a Herdr workspace as `herdr workspace list` reports it. Agent
// runs never use the human's workspaces: herdr-cron creates its own, one per
// `cwd`, because a freshly started headless server has zero of them
// (docs/spec/07-herdr-integration.md §2.4).
type Workspace struct {
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
	ActiveTabID string `json:"active_tab_id"`
}

// Tab is one tab inside a Workspace. herdr-cron creates exactly one per run and
// closes it when the run ends, so a tab is the unit of run isolation
// (docs/spec/07-herdr-integration.md §3).
type Tab struct {
	TabID       string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
}

// Pane is where an agent actually runs. Cwd is the field the trust pre-flight
// and worktree membership are decided from, and it is only reliably present
// from herdr 0.7.5 onward — below the 0.8.2 floor this client enforces.
type Pane struct {
	PaneID      string `json:"pane_id"`
	TabID       string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`
	Cwd         string `json:"cwd"`
}

type workspaceListEnvelope struct {
	Type       string      `json:"type"`
	Workspaces []Workspace `json:"workspaces"`
}

type workspaceCreatedEnvelope struct {
	Type      string    `json:"type"`
	Workspace Workspace `json:"workspace"`
	Tab       Tab       `json:"tab"`
	RootPane  Pane      `json:"root_pane"`
}

type tabCreatedEnvelope struct {
	Type     string `json:"type"`
	Tab      Tab    `json:"tab"`
	RootPane Pane   `json:"root_pane"`
}

type paneListEnvelope struct {
	Type  string `json:"type"`
	Panes []Pane `json:"panes"`
}

// Workspaces lists every workspace on the session's server.
func (c *Client) Workspaces(ctx context.Context) ([]Workspace, error) {
	var out workspaceListEnvelope
	err := c.call(ctx, &out, "workspace", "list")
	return out.Workspaces, err
}

// Panes lists every pane on the session's server, across all workspaces. It is
// how a run finds the pane it just created and how cleanup confirms the pane is
// gone.
func (c *Client) Panes(ctx context.Context) ([]Pane, error) {
	var out paneListEnvelope
	err := c.call(ctx, &out, "pane", "list")
	return out.Panes, err
}

// EnsureHostWorkspace finds or creates herdr-cron's own workspace. A freshly started
// headless server has zero workspaces, so there is no `w1` to split
// (docs/spec/07-herdr-integration.md §2.4).
func (c *Client) EnsureHostWorkspace(ctx context.Context, cwd string) (string, error) {
	list, err := c.Workspaces(ctx)
	if err != nil {
		return "", err
	}
	for _, w := range list {
		if w.Label == HostWorkspaceLabel {
			return w.WorkspaceID, nil
		}
	}
	var out workspaceCreatedEnvelope
	// --no-focus is mandatory: a scheduler never steals focus.
	err = c.call(ctx, &out, "workspace", "create",
		"--label", HostWorkspaceLabel, "--cwd", cwd, "--no-focus")
	if err != nil {
		return "", err
	}
	return out.Workspace.WorkspaceID, nil
}

// CreateRunTab makes the tab one run lives in. Panes are never reused: a pane's cwd is
// fixed at creation and `agent start` has no --cwd, and it requires a pane sitting at its
// interactive shell prompt (docs/spec/07-herdr-integration.md §2.4).
func (c *Client) CreateRunTab(ctx context.Context, workspaceID, cwd, label string, env map[string]string) (Tab, Pane, error) {
	args := []string{"tab", "create", "--workspace", workspaceID, "--label", label, "--no-focus"}
	if cwd != "" {
		args = append(args, "--cwd", cwd)
	}
	for k, v := range env {
		args = append(args, "--env", k+"="+v)
	}
	var out tabCreatedEnvelope
	if err := c.call(ctx, &out, args...); err != nil {
		return Tab{}, Pane{}, err
	}
	return out.Tab, out.RootPane, nil
}

// ResolveSession maps a job's `agent.session` onto a client.
//
// "current" follows Herdr's own order — HERDR_SESSION, else the session named by
// HERDR_SOCKET_PATH, else the default session, in which case --session is omitted from
// the argv entirely (docs/spec/07-herdr-integration.md §2.6).
func ResolveSession(spec string) string {
	switch spec {
	case "", DefaultSession:
		return DefaultSession
	case "current":
		if v := os.Getenv("HERDR_SESSION"); v != "" {
			return v
		}
		return "" // the default session: omit --session
	default:
		return spec
	}
}
