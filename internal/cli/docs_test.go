package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// docFlag matches a long flag as it appears in prose or a code block. It stops
// at "=" so that "--driver=daemon" is checked as "--driver".
var docFlag = regexp.MustCompile(`--([a-z][a-z0-9-]*)`)

// foreignCommand marks a documentation line as belonging to another program
// (`herdr plugin list --json`, `go test -race`, `golangci-lint run --fix`).
// Its flags are not ours to validate.
var foreignCommand = regexp.MustCompile("(?:^|[\\s`(|$])(?:herdr|git|gh|jq|curl|docker|npm|npx|node|skills|go|make|systemctl|launchctl|schtasks|golangci-lint|gofumpt|goreleaser|python3|sh|bash|cat|tail|diff)\\s")

// docsAllowedFlags are documented flags with no counterpart in the tree:
// cobra adds --help and its shorthand itself, only at execute time.
var docsAllowedFlags = map[string]struct{}{
	"help": {},
}

// TestDocsNameOnlyRealFlags is the flag-drift guard: prose that promises a flag
// the binary does not have is worse than no prose at all, because an agent will
// run it and get exit 2.
func TestDocsNameOnlyRealFlags(t *testing.T) {
	t.Parallel()

	known := knownFlags(NewRootCommand(BuildInfo{}))
	for _, doc := range documentedFiles(t) {
		t.Run(doc, func(t *testing.T) {
			t.Parallel()

			text, ok := readDoc(t, doc)
			if !ok {
				return
			}
			for i, line := range strings.Split(text, "\n") {
				if foreignCommand.MatchString(line) {
					continue
				}
				for _, m := range docFlag.FindAllStringSubmatch(line, -1) {
					name := m[1]
					if _, allowed := docsAllowedFlags[name]; allowed {
						continue
					}
					if _, exists := known[name]; !exists {
						t.Errorf("%s:%d documents --%s, which no command defines (flags: %s)",
							doc, i+1, name, strings.Join(sortedKeys(known), ", "))
					}
				}
			}
		})
	}
}

// TestDocsInvokeRealCommandsWithTheirOwnFlags is the stronger half of the
// drift guard.
//
// Checking that a flag exists *somewhere* in the tree is not enough: an agent
// reading SKILL.md runs a specific command, and `herdr-cron job list --wait`
// would pass that weaker check while failing at the terminal with exit 2. This
// resolves each documented invocation against the real tree and asserts the
// flags on that line belong to that command.
func TestDocsInvokeRealCommandsWithTheirOwnFlags(t *testing.T) {
	t.Parallel()

	for _, doc := range documentedFiles(t) {
		t.Run(doc, func(t *testing.T) {
			t.Parallel()

			text, ok := readDoc(t, doc)
			if !ok {
				return
			}
			root := NewRootCommand(BuildInfo{})
			for line, invocations := range docCommandLines(text) {
				for _, args := range invocations {
					checkInvocation(t, root, doc, line, args)
				}
			}
		})
	}
}

// TestReadmeCoversEveryCommand keeps the reference section honest. SKILL.md is
// deliberately narrower — it documents only the agent-facing subset — so the
// coverage half of the guard applies to the README alone.
func TestReadmeCoversEveryCommand(t *testing.T) {
	t.Parallel()

	for _, doc := range []string{"README.md", "README.ko.md"} {
		text, ok := readDoc(t, doc)
		if !ok {
			continue
		}
		for _, path := range commandPaths(NewRootCommand(BuildInfo{})) {
			if !strings.Contains(text, path) {
				t.Errorf("%s never mentions `%s`", doc, path)
			}
		}
	}
}

// documentedFiles lists every document that names this CLI's surface.
//
// A translation is not exempt: a Korean reader who runs a flag that does not
// exist gets exit 2 in the same way, and an agent reading a translated example
// gets it wrong in the same way. The skill itself stays English-only, so there
// is exactly one file for an agent to load.
//
// docs/spec/ and docs/research/ are deliberately absent. Those are dated,
// normative documents with their own recorded open points; they describe what
// was specified, not what is installed, and rewriting history to satisfy a
// linter would destroy their value.
func documentedFiles(t *testing.T) []string {
	t.Helper()

	files := []string{
		"README.md",
		"README.ko.md",
		"CONTRIBUTING.md",
		"CONTEXT.md",
		filepath.Join("skills", "herdr-cron", "SKILL.md"),
		filepath.Join("skills", "herdr-cron", "references", "job-schema.md"),
		filepath.Join("skills", "herdr-cron", "references", "json-shapes.md"),
		filepath.Join("skills", "herdr-cron", "references", "troubleshooting.md"),
	}
	// Every ADR, so a new one is covered the day it is written.
	adrs, err := filepath.Glob(filepath.Join("..", "..", "docs", "adr", "*.md"))
	if err != nil {
		t.Fatalf("glob docs/adr: %v", err)
	}
	sort.Strings(adrs)
	for _, p := range adrs {
		rel, err := filepath.Rel(filepath.Join("..", ".."), p)
		if err != nil {
			t.Fatalf("relativise %s: %v", p, err)
		}
		files = append(files, rel)
	}
	return files
}

// readDoc loads a repository-root document. A doc that does not exist yet is
// skipped rather than failed: the guard protects against drift, it does not
// mandate a file layout.
func readDoc(t *testing.T, rel string) (string, bool) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", rel))
	if err != nil {
		t.Skipf("%s is not present: %v", rel, err)
		return "", false
	}
	return string(data), true
}

// knownFlags collects every long flag defined anywhere in the tree.
func knownFlags(root *cobra.Command) map[string]struct{} {
	flags := make(map[string]struct{})
	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		cmd.InitDefaultHelpFlag()
		cmd.Flags().VisitAll(func(f *pflag.Flag) { flags[f.Name] = struct{}{} })
		cmd.PersistentFlags().VisitAll(func(f *pflag.Flag) { flags[f.Name] = struct{}{} })
		for _, sub := range cmd.Commands() {
			walk(sub)
		}
	}
	walk(root)
	return flags
}

// commandPaths lists every runnable command as it is written in the docs,
// e.g. "herdr-cron service uninstall".
func commandPaths(root *cobra.Command) []string {
	var out []string
	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		for _, sub := range cmd.Commands() {
			if sub.Hidden || sub.Name() == "help" || sub.Name() == "completion" {
				continue
			}
			out = append(out, sub.CommandPath())
			walk(sub)
		}
	}
	walk(root)
	return out
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// inlineCode captures a `backticked` span, where a doc often writes a whole
// command inline: `herdr-cron daemon --detach`.
var inlineCode = regexp.MustCompile("`([^`]+)`")

// codeFence matches the opening or closing line of a fenced block, with or
// without an info string.
var codeFence = regexp.MustCompile("^\\s*(```+|~~~+)")

// docCommandLines returns every place a document actually invokes this binary:
// a line inside a fenced block that starts with the command, and any inline
// code span that does.
//
// Fence state is tracked rather than assumed. Prose that merely opens with the
// binary's name — "herdr-cron schedules two kinds of work: …", the first
// sentence of SKILL.md — is a sentence, not an invocation, and parsing it as
// one produced a false failure claiming `schedules` was not a command.
func docCommandLines(text string) map[int][]string {
	out := make(map[int][]string)
	inFence := false
	for i, line := range strings.Split(text, "\n") {
		n := i + 1
		if codeFence.MatchString(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			trimmed := strings.TrimSpace(line)
			trimmed = strings.TrimPrefix(trimmed, "$ ")
			if cmd, ok := commandInvocation(trimmed); ok {
				out[n] = append(out[n], cmd)
			}
		}
		for _, m := range inlineCode.FindAllStringSubmatch(line, -1) {
			if cmd, ok := commandInvocation(strings.TrimSpace(m[1])); ok {
				out[n] = append(out[n], cmd)
			}
		}
	}
	return out
}

// commandInvocation reports the argument text when s starts with the binary.
func commandInvocation(s string) (string, bool) {
	const bin = "herdr-cron"
	// A relative invocation is the same command: CONTRIBUTING.md tells a
	// developer to run the freshly built `bin/herdr-cron`.
	for _, prefix := range []string{"bin/" + bin, "./" + bin, bin} {
		if s != prefix && !strings.HasPrefix(s, prefix+" ") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(s, prefix))
		// Drop a trailing shell comment and a line-continuation backslash.
		if i := strings.Index(rest, " #"); i >= 0 {
			rest = rest[:i]
		}
		rest = strings.TrimSuffix(strings.TrimSpace(rest), "\\")
		return strings.TrimSpace(rest), true
	}
	return "", false
}

// checkInvocation resolves one documented command line and validates its flags.
func checkInvocation(t *testing.T, root *cobra.Command, doc string, line int, args string) {
	t.Helper()

	cmd, rest := resolveDocCommand(root, args)
	if cmd == nil {
		// A bare `herdr-cron`, or `herdr-cron --skill`, is the root itself.
		if strings.HasPrefix(strings.TrimSpace(args), "-") || args == "" {
			cmd, rest = root, args
		} else {
			t.Errorf("%s:%d invokes `herdr-cron %s`, which is not a command", doc, line, args)
			return
		}
	}
	own := ownFlags(cmd)
	for _, f := range docFlag.FindAllStringSubmatch(rest, -1) {
		name := f[1]
		if _, allowed := docsAllowedFlags[name]; allowed {
			continue
		}
		if _, exists := own[name]; !exists {
			t.Errorf("%s:%d gives `%s` the flag --%s, which it does not define (it has: %s)",
				doc, line, cmd.CommandPath(), name, strings.Join(sortedKeys(own), ", "))
		}
	}
}

// resolveDocCommand walks a documented path like "service install" down the
// tree, returning the deepest command it matched and the remaining arguments.
func resolveDocCommand(root *cobra.Command, args string) (*cobra.Command, string) {
	cur := root
	fields := strings.Fields(args)
	matched := 0
	for _, name := range fields {
		var next *cobra.Command
		for _, sub := range cur.Commands() {
			if sub.Name() == name {
				next = sub
				break
			}
		}
		if next == nil {
			break
		}
		cur = next
		matched++
	}
	if matched == 0 {
		return nil, ""
	}
	return cur, strings.Join(fields[matched:], " ")
}

// ownFlags collects the flags a single command accepts: its own, plus every
// persistent flag inherited from its ancestors.
func ownFlags(cmd *cobra.Command) map[string]struct{} {
	flags := make(map[string]struct{})
	cmd.InitDefaultHelpFlag()
	cmd.Flags().VisitAll(func(f *pflag.Flag) { flags[f.Name] = struct{}{} })
	cmd.InheritedFlags().VisitAll(func(f *pflag.Flag) { flags[f.Name] = struct{}{} })
	return flags
}
