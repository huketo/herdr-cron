package store

import (
	"os"
	"time"

	"github.com/gofrs/flock"
)

// SetEnabled records or clears an enabled override under overrides.lock, the one state
// file with more than one writer (docs/spec/04-storage.md §4).
//
// declared is the value currently in jobs.yaml; storing it is what lets a later hand edit
// invalidate the override (docs/spec/03-job-model.md §5).
func (s *Store) SetEnabled(jobID string, enabled, declared bool, reason string) error {
	if err := s.roots.EnsureState(); err != nil {
		return err
	}
	lock := flock.New(s.roots.OverridesLock())
	if err := lock.Lock(); err != nil {
		return err
	}
	defer func() { _ = lock.Unlock() }()

	ov, err := s.LoadOverrides()
	if err != nil {
		return err
	}
	if enabled == declared {
		// The requested state equals the file's; an override would be noise.
		delete(ov.Overrides, jobID)
	} else {
		ov.Overrides[jobID] = &Override{
			Enabled:         enabled,
			DeclaredEnabled: declared,
			Reason:          reason,
			At:              time.Now(),
		}
	}
	return s.writeAtomic(s.roots.OverridesFile(), ov)
}

// ForgetJob drops a job's state and override, used by `job rm`.
func (s *Store) ForgetJob(jobID string, purgeHistory bool) error {
	// The lock file lives in the state root, which may not exist yet on a machine where
	// nothing has run.
	if err := s.roots.EnsureState(); err != nil {
		return err
	}
	lock := flock.New(s.roots.OverridesLock())
	if err := lock.Lock(); err != nil {
		return err
	}
	ov, err := s.LoadOverrides()
	if err != nil {
		_ = lock.Unlock()
		return err
	}
	delete(ov.Overrides, jobID)
	err = s.writeAtomic(s.roots.OverridesFile(), ov)
	_ = lock.Unlock()
	if err != nil {
		return err
	}

	if !purgeHistory {
		return nil
	}
	st, err := s.LoadState()
	if err != nil {
		return err
	}
	delete(st.Jobs, jobID)
	if err := s.SaveState(st); err != nil {
		return err
	}
	for _, p := range []string{
		s.roots.RunsFile(jobID),
		s.roots.RunsFile(jobID) + ".lock",
		s.roots.RunsFile(jobID) + ".run.lock",
	} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return os.RemoveAll(s.roots.LogDir(jobID))
}
