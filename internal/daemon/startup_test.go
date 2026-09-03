package daemon

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/huketo/herdr-cron/internal/model"
)

// Liveness is the lock plus the heartbeat, and both must be in hand before any job runs. A
// catch-up run of a kind: agent job takes minutes, during which `daemon --detach` gave up
// waiting and called a healthy start a failure while `status` reported it stopped (issue #13).
func TestHeartbeatPrecedesTheStartupCatchUp(t *testing.T) {
	at := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	body := fmt.Sprintf("      at: %q\n      catchup_window: 1h\n", at.Format(time.RFC3339))
	d, roots := newDaemonWithJob(t, "slow", body, "sleep 5")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()

	// Wait for the catch-up run to be in flight. Anchoring on the run, not on a timer, is
	// what makes the assertion below about ordering rather than about scheduling luck.
	deadline := time.Now().Add(10 * time.Second)
	var inFlight bool
	for time.Now().Before(deadline) && !inFlight {
		runs, err := d.store.Runs("slow")
		if err != nil {
			t.Fatalf("read runs: %v", err)
		}
		if len(runs) == 1 && runs[0].Status == model.StatusRunning {
			inFlight = true
			break
		}
		if len(runs) == 1 {
			t.Fatalf("catch-up run already finished with %q; the ordering is untested", runs[0].Status)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !inFlight {
		t.Fatal("the startup catch-up run never appeared")
	}

	// The heartbeat has to be readable already: `daemon --detach` and `status` are waiting
	// on exactly this file while the run holds the startup path.
	hb := ReadHeartbeat(roots)
	if hb == nil || hb.HeartbeatAt.IsZero() {
		t.Fatal("no heartbeat while the startup catch-up run was still in flight")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(shutdownGrace + 5*time.Second):
		t.Fatal("Run did not return after cancellation")
	}
}
