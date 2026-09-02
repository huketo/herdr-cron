// Command herdr-cron schedules automated work for coding agents: shell commands
// and prompts driven into Herdr panes.
// See docs/spec/ for the specification this implements.
package main

import (
	"os"

	"github.com/huketo/herdr-cron/internal/cli"
)

// Build metadata, stamped in by the release build:
//
//	go build -ldflags "-X main.version=... -X main.commit=... -X main.date=..."
//
// The same three names appear in Makefile and .goreleaser.yaml. An unstamped
// build — `go install`, or the Herdr `[[build]]` command, which runs plain
// `go build` in a tagless clone — is filled in by cli.BuildInfo.Resolve.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	os.Exit(cli.Execute(os.Args[1:], cli.BuildInfo{Version: version, Commit: commit, Date: date}))
}
