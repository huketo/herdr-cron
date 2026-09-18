package daemon

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"
	"unsafe"

	"github.com/go-co-op/gocron/v2"

	"github.com/huketo/herdr-cron/internal/model"
)

func TestWallElapsedDetectsSuspend(t *testing.T) {
	t.Parallel()
	const wallGap = 47*time.Minute + 49*time.Second
	last := time.Now()
	now := last.Add(wallGap)

	// Go exposes no constructor for divergent wall and monotonic readings. Only
	// this fixture touches Time's private representation; the checks below fail
	// explicitly if the pinned toolchain no longer supports it. No host clock is
	// changed. Model issue #21: 47m49s wall time, but only one active 30s tick.
	value := reflect.ValueOf(&now).Elem()
	wall, ext := value.FieldByName("wall"), value.FieldByName("ext")
	if !wall.IsValid() || wall.Kind() != reflect.Uint64 || wall.Uint()>>63 != 1 ||
		!ext.IsValid() || ext.Kind() != reflect.Int64 || !ext.CanAddr() {
		t.Fatal("cannot construct a suspend timestamp with this time.Time layout")
	}
	monotonic := (*int64)(unsafe.Pointer(ext.UnsafeAddr()))
	*monotonic -= int64(wallGap - clockTick)
	if got := now.Sub(last); got != clockTick {
		t.Fatalf("fixture monotonic gap = %s, want %s", got, clockTick)
	}
	if got := now.UnixNano() - last.UnixNano(); got != int64(wallGap) {
		t.Fatalf("fixture wall gap = %s, want %s", time.Duration(got), wallGap)
	}
	if got := wallElapsed(now, last); got != wallGap {
		t.Fatalf("suspend gap = %s, want %s", got, wallGap)
	}
}

func TestWallElapsedKeepsANormalTickUnderThreshold(t *testing.T) {
	t.Parallel()
	last := time.Now()
	if got := wallElapsed(last.Add(clockTick), last); got != clockTick {
		t.Fatalf("normal tick = %s, want %s", got, clockTick)
	}
}

// Catch-up runs may take minutes. An unrelated Job whose Occurrence arrives
// during recovery must run on time, not wait for catch-up to release its timers.
func TestSleepRecoverySchedulesWhileCatchUpRuns(t *testing.T) {
	for _, kind := range []string{"recurring", "one-shot"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			now := time.Now().UTC().Truncate(time.Second)
			scheduleBody := "      every: 1h\n"
			if kind == "one-shot" {
				scheduleBody = fmt.Sprintf("      at: %q\n", now.Add(-time.Minute).Format(time.RFC3339))
			}
			d, roots := newDaemonWithJob(t, "slow", scheduleBody, "sleep 60")
			body, err := os.ReadFile(roots.JobsFile())
			if err != nil {
				t.Fatal(err)
			}
			body = append(body, []byte(`  - id: upcoming
    schedule:
      cron: "* * * * * *"
      jitter: off
      catchup: off
    kind: shell
    shell:
      command: "echo awake"
`)...)
			if err := os.WriteFile(roots.JobsFile(), body, 0o644); err != nil {
				t.Fatal(err)
			}
			d.reload()
			if d.configErr != nil {
				t.Fatal(*d.configErr)
			}
			state, err := d.store.LoadState()
			if err != nil {
				t.Fatal(err)
			}
			last := now.Add(-2 * time.Hour)
			state.Job("slow").LastScheduledAt = &last
			if err := d.store.SaveState(state); err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithCancel(context.Background())
			d.ctx = ctx
			sched, err := gocron.NewScheduler(gocron.WithLocation(time.UTC))
			if err != nil {
				cancel()
				t.Fatal(err)
			}
			d.sched = sched
			done := make(chan struct{})
			started := false
			t.Cleanup(func() {
				cancel()
				if started {
					select {
					case <-done:
					case <-time.After(10 * time.Second):
						t.Error("sleep recovery did not stop after cancellation")
					}
				}
				if err := sched.Shutdown(); err != nil {
					t.Error(err)
				}
			})

			// Model the post-suspend scheduler: wall time has advanced, but the
			// previously armed timer still has 47 minutes left. Keep gocron,
			// the runner and the store real; only the old deadline is simulated.
			gid := stableID("upcoming")
			_, err = sched.NewJob(gocron.CronJob("* * * * * *", true),
				gocron.NewTask(func() { d.fire("upcoming") }),
				gocron.WithIdentifier(gid),
				gocron.WithStartAt(gocron.WithStartDateTime(time.Now().Add(47*time.Minute))))
			if err != nil {
				t.Fatal(err)
			}
			d.ids["upcoming"] = gid
			sched.Start()
			started = true
			go func() {
				defer close(done)
				d.recoverFromSleep(ctx)
			}()

			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				slow, err := d.store.Runs("slow")
				if err != nil {
					t.Fatal(err)
				}
				upcoming, err := d.store.Runs("upcoming")
				if err != nil {
					t.Fatal(err)
				}
				if len(slow) == 1 && slow[0].Status == model.StatusRunning &&
					slow[0].Trigger == model.TriggerCatchup {
					for _, run := range upcoming {
						if run.Status == model.StatusSuccess && run.Trigger == model.TriggerScheduler {
							return
						}
					}
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Fatal("upcoming Job did not execute while the catch-up Run was in flight")
		})
	}
}
