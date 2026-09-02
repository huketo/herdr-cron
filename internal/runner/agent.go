package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/huketo/herdr-cron/internal/herdr"
	"github.com/huketo/herdr-cron/internal/model"
)

// Preamble is prepended, verbatim, to every scheduled agent prompt. It is not optional
// and not configurable in v1: its absence is the documented cause of a scheduled agent
// stalling forever on a question (docs/spec/03-job-model.md §3.3).
const Preamble = `You are being run by herdr-cron on a schedule. There is no human watching this session.
Do not ask questions; if a required detail is missing, make the safest reasonable assumption
or stop and explain what was missing. Do not wait for approval. When you are done, state the
outcome in one line.`

// agentOutcome carries what the classifier decided plus the Herdr coordinates to record.
type agentOutcome struct {
	status  model.Status
	reason  string
	excerpt string
	herdr   *model.RunHerdr
}

// executeAgent runs a kind: agent job through the Herdr adapter
// (docs/spec/07-herdr-integration.md §3).
func executeAgent(ctx context.Context, job *model.Resolved, runID string, logFile io.Writer) agentOutcome {
	payload, ok := job.Payload.(model.AgentPayload)
	if !ok {
		return agentOutcome{model.StatusFailure, "herdr_unexpected", "malformed agent payload", nil}
	}
	logf := func(format string, args ...any) {
		fmt.Fprintf(logFile, format+"\n", args...)
	}

	// Step 0: resolve the adapter and gate on its version.
	session := herdr.ResolveSession(payload.Session)
	client, err := herdr.New(session)
	if err != nil {
		logf("herdr-cron: %v", err)
		return agentOutcome{model.StatusFailure, "herdr_unavailable", err.Error(), nil}
	}
	if err := client.CheckVersion(ctx); err != nil {
		logf("herdr-cron: %v", err)
		return agentOutcome{model.StatusFailure, reasonFor(err), err.Error(), nil}
	}

	// Step 3, before anything is created: an untrusted cwd costs one file read and
	// leaves nothing behind (docs/spec/07-herdr-integration.md §5.2).
	verdict, terr := herdr.CheckTrust(payload.AgentKind, job.Cwd)
	if terr != nil {
		logf("herdr-cron: trust pre-flight could not run: %v", terr)
	}
	if verdict == herdr.TrustUntrusted {
		logf("%s", herdr.TrustRemediation(job.ID, payload.AgentKind, job.Cwd))
		return agentOutcome{model.StatusBlocked, "cwd_not_trusted",
			fmt.Sprintf("%s has never been trusted for %s", payload.AgentKind, job.Cwd), nil}
	}

	// Steps 1-2: the server, then herdr-cron's own workspace.
	if err := client.EnsureServer(ctx); err != nil {
		logf("herdr-cron: %v", err)
		return agentOutcome{model.StatusFailure, reasonFor(err), err.Error(), nil}
	}
	workspace, err := client.EnsureHostWorkspace(ctx, job.Cwd)
	if err != nil {
		logf("herdr-cron: %v", err)
		return agentOutcome{model.StatusFailure, reasonFor(err), err.Error(), nil}
	}

	// Step 4: one tab per run. Panes are never reused — a pane's cwd is fixed at
	// creation and agent start requires a shell prompt.
	_, pane, err := client.CreateRunTab(ctx, workspace, job.Cwd, "cron/"+job.ID, job.Env)
	if err != nil {
		logf("herdr-cron: cannot create a run tab: %v", err)
		return agentOutcome{model.StatusFailure, reasonFor(err), err.Error(), nil}
	}
	coords := &model.RunHerdr{Session: session, PaneID: pane.PaneID}
	logf("herdr-cron: session=%s workspace=%s pane=%s", displaySession(session), workspace, pane.PaneID)

	// Step 5: the agent alias, unique among live agents.
	name := herdr.AgentName(job.ID, runID)
	if live, err := client.AgentList(ctx); err == nil {
		for _, a := range live {
			if a.Name == name {
				name = herdr.AgentNameForRun(job.ID, runID)
				break
			}
		}
	}
	coords.AgentName = name

	out := runAgent(ctx, client, job, payload, name, pane.PaneID, logFile, logf)
	out.herdr = coords

	// Step 10: a blocked pane is left open, because a human is required and the
	// transcript on screen is the evidence (docs/spec/07-herdr-integration.md §2.5).
	if out.status != model.StatusBlocked {
		if err := client.ClosePane(ctx, pane.PaneID); err != nil {
			logf("herdr-cron: could not close pane %s: %v", pane.PaneID, err)
		}
	} else {
		logf("herdr-cron: leaving pane %s open for inspection", pane.PaneID)
	}
	return out
}

func runAgent(ctx context.Context, client *herdr.Client, job *model.Resolved,
	payload model.AgentPayload, name, paneID string, logFile io.Writer,
	logf func(string, ...any)) agentOutcome {

	// Step 6: start the agent.
	agent, err := client.StartAgent(ctx, herdr.StartAgentRequest{
		Name: name, Kind: payload.AgentKind, PaneID: paneID, TimeoutMS: 90000,
	})
	if err != nil {
		if herdr.Code(err) == "agent_not_ready" {
			// The verified unattended failure: a startup dialog nobody can answer. Capture
			// what is on screen, then release the pane's foreground.
			if snap, rerr := client.Read(ctx, name, herdr.SourceDetection, 60); rerr == nil {
				logf("herdr-cron: startup dialog on screen:\n%s", snap)
			}
			_, _, _, _ = client.Raw(ctx, "agent", "send-keys", name, "esc")
			return agentOutcome{model.StatusBlocked, "agent_startup_dialog", "agent_not_ready", nil}
		}
		logf("herdr-cron: agent start failed: %v", err)
		return agentOutcome{model.StatusFailure, reasonFor(err), err.Error(), nil}
	}
	logf("herdr-cron: agent %s started as %s (%s)", payload.AgentKind, name, agent.AgentStatus)

	// Step 7: prompt and wait in one request.
	timeoutMS := int(job.TimeoutSec * 1000)
	if timeoutMS <= 0 {
		timeoutMS = 30 * 60 * 1000
	}
	text := Preamble + "\n\n" + payload.Prompt
	settled, err := client.Prompt(ctx, name, text, timeoutMS)
	if err != nil {
		status, reason := classifyPromptError(err)
		logf("herdr-cron: prompt failed: %v", err)
		return agentOutcome{status, reason, err.Error(), nil}
	}
	logf("herdr-cron: settled as %s", settled.AgentStatus)

	// Step 8: capture, with the documented fallback chain.
	transcript := ""
	if payload.Capture != "none" {
		transcript = capture(ctx, client, name, logf)
		if transcript != "" {
			fmt.Fprintf(logFile, "\n--- transcript ---\n%s\n", transcript)
		}
	}
	final := herdr.FinalAssistantText(transcript, payload.AgentKind)

	// Step 9: classify.
	switch settled.AgentStatus {
	case herdr.StatusBlocked:
		return agentOutcome{model.StatusBlocked, "agent_blocked", excerpt(final, transcript), nil}
	case herdr.StatusUnknown:
		return agentOutcome{model.StatusTimeout, "agent_unknown", excerpt(final, transcript), nil}
	case herdr.StatusDone:
		return succeed(payload, final, transcript)
	case herdr.StatusIdle:
		// `idle` is weaker than it looks: with no matching rule Herdr falls back to idle,
		// so a stalled dialog can look finished (docs/spec/07-herdr-integration.md §4.1).
		if idleFallbackUnverified(ctx, client, name, final, logf) {
			return agentOutcome{model.StatusFailure, "agent_idle_fallback_unverified",
				"the agent settled to a fallback idle with no assistant output", nil}
		}
		return succeed(payload, final, transcript)
	default:
		return agentOutcome{model.StatusFailure, "herdr_unexpected",
			"unexpected agent_status " + settled.AgentStatus, nil}
	}
}

// capture reads the transcript, backing off when the agent will not serve a scrolled read.
func capture(ctx context.Context, client *herdr.Client, name string, logf func(string, ...any)) string {
	if text, err := client.Read(ctx, name, herdr.SourceRecentUnwrapped, 200); err == nil {
		return text
	} else if herdr.Code(err) != "agent_not_idle" {
		logf("herdr-cron: transcript read failed: %v", err)
		return ""
	}
	if text, err := client.Read(ctx, name, herdr.SourceRecentUnwrapped, 80); err == nil {
		return text
	}
	text, err := client.Read(ctx, name, herdr.SourceDetection, 0)
	if err != nil {
		logf("herdr-cron: transcript unavailable: %v", err)
		return ""
	}
	logf("herdr-cron: capture: partial (detection snapshot)")
	return text
}

// idleFallbackUnverified is the cross-check. It refines, it never gates: an explain call
// that fails must not change the outcome.
func idleFallbackUnverified(ctx context.Context, client *herdr.Client, name, final string,
	logf func(string, ...any)) bool {

	ex, err := client.Explain(ctx, name)
	if err != nil {
		return false
	}
	if ex.VisibleBlocker {
		logf("herdr-cron: explain reports a visible blocker")
	}
	fallback := ex.FallbackReason == herdr.IdleFallback ||
		strings.Contains(ex.Raw, herdr.IdleFallback)
	if ex.FallbackReason != "" {
		logf("herdr-cron: explain fallback_reason=%s", ex.FallbackReason)
	}
	// An empty answer from a fallback-classified idle is the signature of a stalled dialog.
	return fallback && strings.TrimSpace(final) == ""
}

func succeed(payload model.AgentPayload, final, transcript string) agentOutcome {
	// The comparison is exact against the extracted block; a substring search would match
	// the prompt echo (docs/spec/07-herdr-integration.md §4.2).
	if payload.NoOpMarker != "" && final == payload.NoOpMarker {
		return agentOutcome{model.StatusNoOp, "", final, nil}
	}
	return agentOutcome{model.StatusSuccess, "", excerpt(final, transcript), nil}
}

func excerpt(final, transcript string) string {
	if strings.TrimSpace(final) != "" {
		return final
	}
	return tailString(transcript, excerptBytes)
}

func tailString(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

func classifyPromptError(err error) (model.Status, string) {
	switch herdr.Code(err) {
	case "agent_blocked":
		return model.StatusBlocked, "agent_blocked"
	case "agent_prompt_stalled":
		return model.StatusFailure, "agent_prompt_stalled"
	case "timeout":
		return model.StatusTimeout, "wait_timeout"
	case "agent_not_running":
		return model.StatusFailure, "agent_vanished"
	case "pane_not_found":
		return model.StatusFailure, "pane_lost"
	default:
		return model.StatusFailure, reasonFor(err)
	}
}

// reasonFor maps an adapter error onto a run reason. The table is total by fallback,
// because herdr's error codes have no published enum.
func reasonFor(err error) string {
	switch {
	case errors.Is(err, herdr.ErrVersionUnsupported):
		return "herdr_version_unsupported"
	case errors.Is(err, herdr.ErrUnavailable):
		return "herdr_unavailable"
	case errors.Is(err, context.DeadlineExceeded):
		return "job_timeout"
	}
	switch herdr.Code(err) {
	case "agent_not_running":
		return "agent_vanished"
	case "pane_not_found":
		return "pane_lost"
	case "":
		return "herdr_unexpected"
	default:
		return "herdr_unexpected"
	}
}

func displaySession(s string) string {
	if s == "" {
		return "(default)"
	}
	return s
}
