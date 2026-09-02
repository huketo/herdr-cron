package config

import (
	"os"
	"strings"
	"testing"
)

const authored = `version: 1

# 스케줄러가 읽는 파일. 손으로 편집해도 된다.
defaults:
  timezone: Asia/Seoul

jobs:
  # 평일 새벽 의존성 점검
  - id: nightly-deps
    name: Nightly dependency audit
    schedule:
      cron: "17 3 * * 1-5"   # :17, not :00 — jitter is separate
    kind: shell
    shell:
      command: "true"
`

// jobs.yaml is meant to be committed and reviewed, so a write must not eat the comments
// (docs/spec/04-storage.md §3).
func TestApplyPreservesComments(t *testing.T) {
	p := write(t, authored)
	name := "renamed"
	if _, issues, err := Apply(p, UpdateJob(Edit{ID: "nightly-deps", Name: &name})); err != nil || len(issues) > 0 {
		t.Fatalf("apply failed: %v %v", err, issues)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	body := string(got)
	for _, want := range []string{
		"# 스케줄러가 읽는 파일. 손으로 편집해도 된다.",
		"# 평일 새벽 의존성 점검",
		"# :17, not :00",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("comment %q was lost:\n%s", want, body)
		}
	}
	if !strings.Contains(body, "name: renamed") {
		t.Errorf("the edit did not land:\n%s", body)
	}
}

// An edit that would produce an invalid file must leave the original untouched.
func TestApplyRejectsInvalidResultWithoutWriting(t *testing.T) {
	p := write(t, authored)
	before, _ := os.ReadFile(p)

	bad := "not a cron expression at all"
	_, issues, err := Apply(p, UpdateJob(Edit{ID: "nightly-deps", Schedule: &bad}))
	if err == nil && len(issues) == 0 {
		t.Fatal("expected the invalid schedule to be rejected")
	}
	after, _ := os.ReadFile(p)
	if string(before) != string(after) {
		t.Fatalf("the file was modified despite the failure:\n%s", after)
	}
}

func TestAddRejectsDuplicateAndUpdateRejectsMissing(t *testing.T) {
	p := write(t, authored)
	sched := "1h"
	cmd := "true"
	if _, _, err := Apply(p, AddJob(Edit{ID: "nightly-deps", Schedule: &sched, Command: &cmd})); err == nil {
		t.Error("adding a duplicate id must fail")
	}
	if _, _, err := Apply(p, UpdateJob(Edit{ID: "ghost", Schedule: &sched})); err == nil {
		t.Error("updating a missing id must fail")
	}
}

// One --schedule flag, disambiguated by shape (docs/spec/05-cli.md §3.1).
func TestScheduleFormDisambiguation(t *testing.T) {
	for _, tc := range []struct{ in, form string }{
		{"@daily", "cron"},
		{"17 3 * * 1-5", "cron"},
		{"30m", "every"},
		{"2026-12-24T18:00:00+09:00", "at"},
	} {
		form, _, err := scheduleForm(tc.in)
		if err != nil {
			t.Errorf("%q: %v", tc.in, err)
			continue
		}
		if form != tc.form {
			t.Errorf("%q resolved to %q, want %q", tc.in, form, tc.form)
		}
	}
	if _, _, err := scheduleForm("nonsense"); err == nil {
		t.Error("an unparseable expression must be rejected")
	}
}

// Switching kind must remove the other kind's payload, or validation would reject the
// result (docs/spec/03-job-model.md §1.2).
func TestSwitchingKindDropsTheOtherPayload(t *testing.T) {
	p := write(t, authored)
	prompt := "Audit the dependencies and stop."
	if _, issues, err := Apply(p, UpdateJob(Edit{ID: "nightly-deps", Prompt: &prompt})); err != nil || len(issues) > 0 {
		t.Fatalf("apply failed: %v %v", err, issues)
	}
	body, _ := os.ReadFile(p)
	if strings.Contains(string(body), "shell:") {
		t.Errorf("the shell payload survived a switch to kind: agent:\n%s", body)
	}
	loaded, errs := Load(p)
	if len(errs) > 0 {
		t.Fatalf("result does not validate: %v", errs)
	}
	j, _ := loaded.Job("nightly-deps")
	if j.Kind != "agent" {
		t.Errorf("kind = %q, want agent", j.Kind)
	}
}
