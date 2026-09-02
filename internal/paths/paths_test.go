package paths

import (
	"path/filepath"
	"testing"
)

// clearEnv blanks every variable Resolve consults, so a test states its whole
// input rather than inheriting the developer's shell — which is how a
// HERDR_ENV=1 terminal made the plugin-root bug invisible locally.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"XDG_CONFIG_HOME", "XDG_STATE_HOME",
		"HERDR_CRON_HOME", "HERDR_CRON_STATE_DIR", "HERDR_CRON_CONFIG",
		"HERDR_PLUGIN_STATE_DIR",
	} {
		t.Setenv(k, "")
	}
}

// TestPluginStateDirIsIgnored is the regression guard for a duplicate-execution
// bug found by running the released plugin next to the standalone CLI.
//
// Resolve used to take HERDR_PLUGIN_STATE_DIR as the state root. Herdr sets
// that variable for every [[startup]] hook and every action, so the daemon
// Herdr started resolved a different state root from the daemon a human
// started — while both read the same jobs.yaml. daemon.lock lives in the state
// root, so the single-instance lock guarded nothing: two daemons ran at once
// and every occurrence fired twice, which for a kind: agent job is two bills.
//
// The state root MUST depend only on the machine, never on which front door
// started the process.
func TestPluginStateDirIsIgnored(t *testing.T) {
	clearEnv(t)

	plain, err := Resolve(Overrides{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	t.Setenv("HERDR_PLUGIN_STATE_DIR", filepath.Join(t.TempDir(), "plugin-state"))
	underHerdr, err := Resolve(Overrides{})
	if err != nil {
		t.Fatalf("Resolve under Herdr: %v", err)
	}

	if underHerdr.State != plain.State {
		t.Errorf("state root changed because HERDR_PLUGIN_STATE_DIR was set:\n  plugin front door: %s\n  standalone CLI:    %s\n"+
			"two daemons would then hold two different daemon.lock files and every job would run twice",
			underHerdr.State, plain.State)
	}
	if underHerdr.Config != plain.Config {
		t.Errorf("config root changed under Herdr: %s vs %s", underHerdr.Config, plain.Config)
	}
}

func TestResolvePrecedence(t *testing.T) {
	xdgCfg := t.TempDir()
	xdgState := t.TempDir()
	cronHome := t.TempDir()
	stateDir := t.TempDir()
	flagState := t.TempDir()

	tests := []struct {
		name      string
		env       map[string]string
		ov        Overrides
		wantCfg   string
		wantState string
		wantJobs  string
	}{
		{
			name:      "XDG wins over the per-OS default",
			env:       map[string]string{"XDG_CONFIG_HOME": xdgCfg, "XDG_STATE_HOME": xdgState},
			wantCfg:   filepath.Join(xdgCfg, appName),
			wantState: filepath.Join(xdgState, appName),
			wantJobs:  filepath.Join(xdgCfg, appName, "jobs.yaml"),
		},
		{
			// The variable to use for a test or a throwaway install: it moves
			// both roots together, which is the property the plugin state dir
			// lacked.
			name:      "HERDR_CRON_HOME sets both roots",
			env:       map[string]string{"XDG_CONFIG_HOME": xdgCfg, "HERDR_CRON_HOME": cronHome},
			wantCfg:   filepath.Join(cronHome, "config"),
			wantState: filepath.Join(cronHome, "state"),
			wantJobs:  filepath.Join(cronHome, "config", "jobs.yaml"),
		},
		{
			name:      "HERDR_CRON_STATE_DIR beats HERDR_CRON_HOME",
			env:       map[string]string{"HERDR_CRON_HOME": cronHome, "HERDR_CRON_STATE_DIR": stateDir},
			wantCfg:   filepath.Join(cronHome, "config"),
			wantState: stateDir,
			wantJobs:  filepath.Join(cronHome, "config", "jobs.yaml"),
		},
		{
			name:      "HERDR_PLUGIN_STATE_DIR is inert even beside HERDR_CRON_HOME",
			env:       map[string]string{"HERDR_CRON_HOME": cronHome, "HERDR_PLUGIN_STATE_DIR": stateDir},
			wantCfg:   filepath.Join(cronHome, "config"),
			wantState: filepath.Join(cronHome, "state"),
			wantJobs:  filepath.Join(cronHome, "config", "jobs.yaml"),
		},
		{
			name:      "flags beat every variable",
			env:       map[string]string{"HERDR_CRON_HOME": cronHome, "HERDR_CRON_STATE_DIR": stateDir},
			ov:        Overrides{ConfigFile: filepath.Join(xdgCfg, "custom.yaml"), StateDir: flagState},
			wantCfg:   xdgCfg,
			wantState: flagState,
			wantJobs:  filepath.Join(xdgCfg, "custom.yaml"),
		},
		{
			// HERDR_CRON_CONFIG names a file, so the config root is its parent
			// and JobsFile is the file itself rather than <root>/jobs.yaml.
			name:      "HERDR_CRON_CONFIG names the file, not the directory",
			env:       map[string]string{"HERDR_CRON_CONFIG": filepath.Join(xdgCfg, "elsewhere.yaml")},
			wantCfg:   xdgCfg,
			wantState: "",
			wantJobs:  filepath.Join(xdgCfg, "elsewhere.yaml"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			got, err := Resolve(tc.ov)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if got.Config != tc.wantCfg {
				t.Errorf("Config = %q, want %q", got.Config, tc.wantCfg)
			}
			if tc.wantState != "" && got.State != tc.wantState {
				t.Errorf("State = %q, want %q", got.State, tc.wantState)
			}
			if got.JobsFile() != tc.wantJobs {
				t.Errorf("JobsFile() = %q, want %q", got.JobsFile(), tc.wantJobs)
			}
		})
	}
}

// TestStatePathsStayUnderTheStateRoot is the invariant every accessor shares:
// one --state-dir relocates the whole tree, so a throwaway run can never touch
// the real history.
func TestStatePathsStayUnderTheStateRoot(t *testing.T) {
	clearEnv(t)

	root := t.TempDir()
	r, err := Resolve(Overrides{StateDir: root})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	for name, got := range map[string]string{
		"StateFile":     r.StateFile(),
		"OverridesFile": r.OverridesFile(),
		"OverridesLock": r.OverridesLock(),
		"DaemonFile":    r.DaemonFile(),
		"TriggersDir":   r.TriggersDir(),
		"TmpDir":        r.TmpDir(),
		"LogDir":        r.LogDir("j"),
		"RunsFile":      r.RunsFile("j"),
		"LogFile":       r.LogFile("j", "run1"),
	} {
		rel, err := filepath.Rel(root, got)
		if err != nil || rel == ".." || filepath.IsAbs(rel) || len(rel) > 2 && rel[:3] == ".."+string(filepath.Separator) {
			t.Errorf("%s() = %q, which escapes the state root %q", name, got, root)
		}
	}

	// The recorded form is relative and slash-separated, so a run record stays
	// portable across platforms.
	if got := r.LogFileRel("j", "run1"); got != "logs/j/run1.log" {
		t.Errorf("LogFileRel() = %q, want logs/j/run1.log", got)
	}
}
