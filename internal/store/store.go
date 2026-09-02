// Package store implements the file-backed persistence of docs/spec/04-storage.md.
package store

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"

	"github.com/huketo/herdr-cron/internal/model"
	"github.com/huketo/herdr-cron/internal/paths"
)

// Store owns every read and write under the state root.
type Store struct {
	roots paths.Roots
}

// New binds a Store to already-resolved roots. It touches no disk; the state directories are
// created by paths.Roots.EnsureState before the first write.
func New(r paths.Roots) *Store { return &Store{roots: r} }

// Roots exposes the resolved directories, so a caller that needs to name a path — a log file
// to hand to a pager, jobs.yaml to edit — does not resolve the roots a second time and risk
// disagreeing with the Store about where state lives.
func (s *Store) Roots() paths.Roots { return s.roots }

// ---------------------------------------------------------------- state.json

// State is the whole of state.json, keyed by job id. Its only legitimate writer is the
// executing scheduler process — the daemon under the daemon and foreground drivers, one
// short-lived run-once process under os-scheduler — which is why it carries no lock, only
// atomic replacement (docs/spec/04-storage.md §4, §9). The effective-enabled override
// deliberately does not live here; see Overrides.
type State struct {
	Version   int                  `json:"version"`
	UpdatedAt time.Time            `json:"updatedAt"`
	Jobs      map[string]*JobState `json:"jobs"`
}

// JobState is one job's entry in state.json. LastScheduledAt is the catch-up watermark and is
// written *before* the occurrences it claims execute, so a crash mid-pass re-runs at most what
// it had not yet claimed (docs/spec/03-job-model.md §4.2). ConsecutiveFailures feeds the
// auto-disable breaker and counts failure, timeout and blocked alike (§4.5).
type JobState struct {
	LastScheduledAt     *time.Time   `json:"lastScheduledAt,omitempty"`
	LastRunID           string       `json:"lastRunId,omitempty"`
	LastStatus          model.Status `json:"lastStatus,omitempty"`
	LastFinishedAt      *time.Time   `json:"lastFinishedAt,omitempty"`
	ConsecutiveFailures int          `json:"consecutiveFailures"`
	RunsToday           *RunsToday   `json:"runsToday,omitempty"`
}

// RunsToday is the max_runs_per_day counter. Date is the calendar day in the job's timezone;
// when it is not today the count is stale and restarts at zero, which is why the day is stored
// alongside it rather than inferred (docs/spec/03-job-model.md §4.5).
type RunsToday struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
}

// LoadState reads state.json, tolerating one torn read, and reports a missing file as empty
// state: a first run has no state and that is not an error. A file that exists but does not
// parse *is* an error — starting from zero would silently rewind every catch-up watermark.
func (s *Store) LoadState() (*State, error) {
	st := &State{Version: 1, Jobs: map[string]*JobState{}}
	b, err := readRetry(s.roots.StateFile())
	if err != nil {
		if os.IsNotExist(err) {
			return st, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(b, st); err != nil {
		return nil, fmt.Errorf("state.json is corrupt: %w", err)
	}
	if st.Jobs == nil {
		st.Jobs = map[string]*JobState{}
	}
	return st, nil
}

// SaveState atomically replaces state.json, stamping version and updatedAt. Legitimate only
// from the executing scheduler process; a second writer would lose the other's watermark
// (docs/spec/04-storage.md §9).
func (s *Store) SaveState(st *State) error {
	st.Version = 1
	st.UpdatedAt = time.Now()
	return s.writeAtomic(s.roots.StateFile(), st)
}

// Job returns the entry for a job, creating it if absent, so a caller can record an outcome
// into a job the state file has never seen without a nil check.
func (st *State) Job(id string) *JobState {
	js, ok := st.Jobs[id]
	if !ok {
		js = &JobState{}
		st.Jobs[id] = js
	}
	return js
}

// ------------------------------------------------------------ overrides.json

// Overrides is the whole of overrides.json — the effective-enabled layer of
// docs/spec/03-job-model.md §5, held apart from State on purpose. It is the one state file
// with more than one legitimate writer (the CLI, the TUI and the daemon), each holding
// overrides.lock across the whole read-modify-write, and that is what lets job pause work with
// no daemon running at all (docs/spec/04-storage.md §4, docs/spec/05-cli.md §4). Keeping the
// bit here is also why a TUI toggle never rewrites the user's jobs.yaml.
type Overrides struct {
	Version   int                  `json:"version"`
	Overrides map[string]*Override `json:"overrides"`
}

// Override is one job's enabled override. DeclaredEnabled records what jobs.yaml said when the
// override was written, which is what lets a later hand edit of the file invalidate it; Reason
// is manual or auto_failures. Entries for job ids no longer in jobs.yaml are retained 30 days,
// so a git checkout does not destroy them and a rename away and back does not resurrect them
// (docs/spec/04-storage.md §4).
type Override struct {
	Enabled         bool      `json:"enabled"`
	DeclaredEnabled bool      `json:"declaredEnabled"`
	Reason          string    `json:"reason"`
	At              time.Time `json:"at"`
}

// LoadOverrides reads overrides.json, reporting a missing file as no overrides. It takes no
// lock of its own — SetEnabled and ForgetJob call it while already holding overrides.lock, and
// a read-only client absorbs a torn read by retrying, which is cheaper than a reader lock and
// sufficient because every writer renames atomically (docs/spec/04-storage.md §9).
func (s *Store) LoadOverrides() (*Overrides, error) {
	o := &Overrides{Version: 1, Overrides: map[string]*Override{}}
	b, err := readRetry(s.roots.OverridesFile())
	if err != nil {
		if os.IsNotExist(err) {
			return o, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(b, o); err != nil {
		return nil, fmt.Errorf("overrides.json is corrupt: %w", err)
	}
	if o.Overrides == nil {
		o.Overrides = map[string]*Override{}
	}
	return o, nil
}

// EffectiveEnabled applies docs/spec/03-job-model.md §5. A stale override — one recorded
// against a different declared value — is discarded, so editing jobs.yaml always wins back.
func EffectiveEnabled(declared bool, ov *Override) (bool, string) {
	if ov != nil && ov.DeclaredEnabled == declared {
		return ov.Enabled, "override"
	}
	return declared, "file"
}

// --------------------------------------------------------------- run history

// AppendRun appends one run record under a per-job advisory lock, so concurrent
// run-once processes cannot interleave a line.
func (s *Store) AppendRun(r *model.Run) error {
	path := s.roots.RunsFile(r.JobID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	lock := flock.New(path + ".lock")
	if err := lock.Lock(); err != nil {
		return err
	}
	defer func() { _ = lock.Unlock() }()

	line, err := r.MarshalLine()
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = f.Write(line)
	return err
}

// Runs reads a job's history, reducing by runId so a terminal record replaces its
// "running" partner.
func (s *Store) Runs(jobID string) ([]*model.Run, error) {
	f, err := os.Open(s.roots.RunsFile(jobID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	order := []string{}
	byID := map[string]*model.Run{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var r model.Run
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			continue // a torn final line is not a reason to lose the file
		}
		if _, seen := byID[r.RunID]; !seen {
			order = append(order, r.RunID)
		}
		rr := r
		byID[r.RunID] = &rr
	}
	out := make([]*model.Run, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id])
	}
	return out, sc.Err()
}

// TryLockRun takes the per-job execution lock. ok is false when another run holds it,
// which is what "concurrency: skip" observes.
func (s *Store) TryLockRun(jobID string) (unlock func(), ok bool, err error) {
	if err := s.roots.EnsureState(); err != nil {
		return nil, false, err
	}
	if err := os.MkdirAll(filepath.Dir(s.roots.RunsFile(jobID)), 0o755); err != nil {
		return nil, false, err
	}
	lock := flock.New(s.roots.RunsFile(jobID) + ".run.lock")
	got, err := lock.TryLock()
	if err != nil {
		return nil, false, err
	}
	if !got {
		return nil, false, nil
	}
	return func() { _ = lock.Unlock() }, true, nil
}

// --------------------------------------------------------------------- logs

// LogWriter creates the run log file and returns it for streaming writes.
func (s *Store) LogWriter(jobID, runID string) (*os.File, string, error) {
	path := s.roots.LogFile(jobID, runID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, "", err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, "", err
	}
	return f, s.roots.LogFileRel(jobID, runID), nil
}

// ------------------------------------------------------------------ plumbing

func (s *Store) writeAtomic(path string, v any) error {
	if err := s.roots.EnsureState(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')

	tmp, err := os.CreateTemp(s.roots.TmpDir(), filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()

	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.Rename(name, path); err == nil {
		return nil
	}
	// Windows: an indexer or antivirus may hold a transient handle. Retry once.
	time.Sleep(50 * time.Millisecond)
	return os.Rename(name, path)
}

// readRetry tolerates a torn read of an atomically replaced file by retrying once.
func readRetry(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err == nil || os.IsNotExist(err) {
		return b, err
	}
	time.Sleep(50 * time.Millisecond)
	return os.ReadFile(path)
}
