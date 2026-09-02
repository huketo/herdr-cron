package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/huketo/herdr-cron/internal/model"
	"github.com/huketo/herdr-cron/internal/service"
)

type serviceResult struct {
	Type      string          `json:"type"`
	Driver    service.Driver  `json:"driver"`
	Backend   string          `json:"backend"`
	Installed bool            `json:"installed"`
	Entries   []service.Entry `json:"entries"`
	Warnings  []string        `json:"warnings,omitempty"`
}

func serviceCmd(g *globals) *cobra.Command {
	cmd := &cobra.Command{Use: "service", Short: "Register herdr-cron with the OS scheduler"}
	cmd.AddCommand(serviceInstallCmd(g), serviceUninstallCmd(g), serviceStatusCmd(g))
	return cmd
}

// request assembles the backend input. The os-scheduler driver needs every enabled job;
// the daemon driver needs none of them.
func (g *globals) serviceRequest(id string, driver service.Driver) (service.Request, error) {
	roots, err := g.roots()
	if err != nil {
		return service.Request{}, failure(id, "io_error", err.Error(), ExitError, nil)
	}
	self, err := os.Executable()
	if err != nil {
		return service.Request{}, failure(id, "io_error", err.Error(), ExitError, nil)
	}
	req := service.Request{Driver: driver, Roots: roots, Binary: self}

	if driver == service.DriverOSScheduler {
		loaded, st, _, ov, lerr := g.loadAll(id)
		if lerr != nil {
			return service.Request{}, lerr
		}
		_ = st
		for _, j := range loaded.Jobs {
			enabled, _ := effectiveEnabled(j, ov)
			if enabled {
				req.Jobs = append(req.Jobs, j)
			}
		}
		// The self-check a backend uses to prove its own schedule translation. It must
		// report the UN-jittered occurrence: the OS scheduler fires at the base time and
		// herdr-cron adds jitter itself, so comparing against a jittered prediction
		// rejects every correct translation.
		req.NextRun = func(j *model.Resolved) (string, error) {
			runs := baseRuns(j, 1)
			if len(runs) == 0 {
				return "", errors.New("no next occurrence")
			}
			return runs[0], nil
		}
	}
	return req, nil
}

func serviceInstallCmd(g *globals) *cobra.Command {
	var driver string
	var now bool
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install the scheduler as an OS service",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			id := "cli:service:install"
			d, err := parseDriver(id, driver)
			if err != nil {
				return err
			}
			backend, err := service.New()
			if err != nil {
				return failure(id, "usage", err.Error(), ExitUsage, nil)
			}
			req, err := g.serviceRequest(id, d)
			if err != nil {
				return err
			}
			entries, warnings, err := backend.Install(req)
			if err != nil {
				return failure(id, "io_error", err.Error(), ExitError, nil)
			}
			res := serviceResult{Type: "service", Driver: d, Backend: backend.Name(),
				Installed: true, Entries: entries, Warnings: warnings}
			if failed := countState(entries, "error"); failed > 0 {
				emit(os.Stdout, g, Envelope{ID: id, Result: res})
				return failure(id, "config_invalid",
					fmt.Sprintf("%d job(s) could not be registered; see result.entries", failed),
					ExitError, entries)
			}
			if now && d == service.DriverDaemon {
				res.Warnings = append(res.Warnings,
					"the unit was enabled with --now; `herdr-cron status` confirms liveness")
			}
			emit(os.Stdout, g, Envelope{ID: id, Result: res})
			return nil
		},
	}
	cmd.Flags().StringVar(&driver, "driver", string(service.DriverDaemon), "daemon | os-scheduler")
	cmd.Flags().BoolVar(&now, "now", false, "start the service immediately where the backend supports it")
	return cmd
}

func serviceUninstallCmd(g *globals) *cobra.Command {
	var driver string
	var yes bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove every artefact herdr-cron registered",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			id := "cli:service:uninstall"
			if !yes {
				return failure(id, "usage", "refusing to uninstall without --yes", ExitUsage, nil)
			}
			d, err := parseDriver(id, driver)
			if err != nil {
				return err
			}
			backend, err := service.New()
			if err != nil {
				return failure(id, "usage", err.Error(), ExitUsage, nil)
			}
			req, err := g.serviceRequest(id, d)
			if err != nil {
				return err
			}
			entries, err := backend.Uninstall(req)
			if err != nil {
				return failure(id, "io_error", err.Error(), ExitError, nil)
			}
			emit(os.Stdout, g, Envelope{ID: id, Result: serviceResult{
				Type: "service", Driver: d, Backend: backend.Name(),
				Installed: false, Entries: entries}})
			return nil
		},
	}
	cmd.Flags().StringVar(&driver, "driver", string(service.DriverDaemon), "daemon | os-scheduler")
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm the removal")
	return cmd
}

func serviceStatusCmd(g *globals) *cobra.Command {
	var driver string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Report what is registered and whether the OS agrees",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			id := "cli:service:status"
			d, err := parseDriver(id, driver)
			if err != nil {
				return err
			}
			backend, err := service.New()
			if err != nil {
				return failure(id, "usage", err.Error(), ExitUsage, nil)
			}
			req, err := g.serviceRequest(id, d)
			if err != nil {
				return err
			}
			entries, err := backend.Status(req)
			if err != nil {
				return failure(id, "io_error", err.Error(), ExitError, nil)
			}
			emit(os.Stdout, g, Envelope{ID: id, Result: serviceResult{
				Type: "service", Driver: d, Backend: backend.Name(),
				Installed: countState(entries, "ok") > 0, Entries: entries}})
			return nil
		},
	}
	cmd.Flags().StringVar(&driver, "driver", string(service.DriverDaemon), "daemon | os-scheduler")
	return cmd
}

func parseDriver(id, v string) (service.Driver, error) {
	switch service.Driver(v) {
	case service.DriverDaemon:
		return service.DriverDaemon, nil
	case service.DriverOSScheduler:
		return service.DriverOSScheduler, nil
	default:
		return "", failure(id, "usage",
			fmt.Sprintf("unknown driver %q; use daemon or os-scheduler", v), ExitUsage, nil)
	}
}

func countState(entries []service.Entry, state string) int {
	n := 0
	for _, e := range entries {
		if e.State == state {
			n++
		}
	}
	return n
}
