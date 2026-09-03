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
	d, roots := newDaemonWithJob(t, "slow", body, "sleep 3")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()

	var hb *Heartbeat
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if hb = ReadHeartbeat(roots); hb != nil && !hb.HeartbeatAt.IsZero() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if hb == nil || hb.HeartbeatAt.IsZero() {
		t.Fatal("no heartbeat while the startup catch-up run was still in flight")
	}

	// The heartbeat is only proof of ordering if the catch-up run has not finished yet.
	runs, err := d.store.Runs("slow")
	if err != nil {
		t.Fatalf("read runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want the catch-up run in flight", len(runs))
	}
	if runs[0].Status != model.StatusRunning {
		t.Fatalf("catch-up run already finished with %q; the ordering is untested", runs[0].Status)
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
