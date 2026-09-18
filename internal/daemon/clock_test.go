package daemon

import (
	"testing"
	"time"
)

// A 47m 49s suspend is the reproduction from issue #21: clockLoop compared ticker
// times with time.Time.Sub, which uses the monotonic reading and never saw the jump.
func TestWallElapsedDetectsSuspend(t *testing.T) {
	t.Parallel()
	last := time.Date(2026, 9, 18, 19, 40, 0, 0, time.UTC)
	now := time.Date(2026, 9, 18, 20, 27, 49, 0, time.UTC)
	gap := wallElapsed(now, last)
	if gap <= sleepThreshold {
		t.Fatalf("47m 49s wall jump reported as %s, want > %s", gap, sleepThreshold)
	}
	if gap != 47*time.Minute+49*time.Second {
		t.Fatalf("gap = %s, want 47m49s", gap)
	}
}

func TestWallElapsedKeepsANormalTickUnderThreshold(t *testing.T) {
	t.Parallel()
	mono := time.Now()
	later := mono.Add(clockTick)
	if later.Sub(mono) > sleepThreshold {
		t.Fatal("monotonic 30s tick should stay under the sleep threshold")
	}
	if wallElapsed(later, mono) > sleepThreshold {
		t.Fatal("wallElapsed of a 30s Add should stay under the sleep threshold")
	}
}
