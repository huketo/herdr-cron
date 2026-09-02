package cli

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/huketo/herdr-cron/skills"
)

// ------------------------------------------------------------------ schema

type schemaResult struct {
	Type     string          `json:"type"`
	Version  string          `json:"version"`
	Commands []commandSchema `json:"commands"`
}

type commandSchema struct {
	Path     string       `json:"path"`
	Name     string       `json:"name"`
	Use      string       `json:"use"`
	Short    string       `json:"short"`
	Args     string       `json:"args,omitempty"`
	Flags    []flagSchema `json:"flags"`
	Runnable bool         `json:"runnable"`
}

type flagSchema struct {
	Name      string `json:"name"`
	Shorthand string `json:"shorthand,omitempty"`
	Type      string `json:"type"`
	Default   string `json:"default,omitempty"`
	Usage     string `json:"usage"`
	Global    bool   `json:"global"`
}

// schemaCmd prints the command tree as JSON so an agent can discover the surface without
// scraping --help (docs/spec/05-cli.md §3.5).
func schemaCmd(g *globals) *cobra.Command {
	var only string
	cmd := &cobra.Command{
		Use:   "schema",
		Short: "Print the command tree as JSON",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			id := "cli:schema"
			root := c.Root()
			res := schemaResult{Type: "schema", Version: versionOr(g.info.Version)}
			walk(root, "", &res.Commands)
			if only != "" {
				want := strings.TrimSpace(only)
				var kept []commandSchema
				for _, cs := range res.Commands {
					if cs.Path == want || strings.HasPrefix(cs.Path, want+" ") {
						kept = append(kept, cs)
					}
				}
				if len(kept) == 0 {
					return failure(id, "usage", fmt.Sprintf("no command %q", want), ExitUsage, nil)
				}
				res.Commands = kept
			}
			emit(os.Stdout, g, Envelope{ID: id, Result: res})
			return nil
		},
	}
	cmd.Flags().StringVar(&only, "command", "", `limit the output to one command path, e.g. "job add"`)
	return cmd
}

func walk(c *cobra.Command, prefix string, out *[]commandSchema) {
	if c.Hidden {
		return
	}
	// Paths are relative to the root, so an agent asks for "job add" rather than
	// "herdr-cron job add"; the root itself is the binary name.
	path := strings.TrimSpace(prefix + " " + c.Name())
	child := path
	if c.Parent() == nil {
		path, child = c.Name(), ""
	}
	cs := commandSchema{
		Path: path, Name: c.Name(), Use: c.UseLine(), Short: c.Short,
		Runnable: c.Runnable(), Flags: []flagSchema{},
	}
	c.LocalFlags().VisitAll(func(f *pflag.Flag) { cs.Flags = append(cs.Flags, describe(f, false)) })
	c.Root().PersistentFlags().VisitAll(func(f *pflag.Flag) { cs.Flags = append(cs.Flags, describe(f, true)) })
	*out = append(*out, cs)

	for _, sub := range c.Commands() {
		walk(sub, child, out)
	}
}

func describe(f *pflag.Flag, global bool) flagSchema {
	return flagSchema{
		Name: f.Name, Shorthand: f.Shorthand, Type: f.Value.Type(),
		Default: f.DefValue, Usage: f.Usage, Global: global,
	}
}

// ------------------------------------------------------------- completion

func completionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:       "completion [bash|zsh|fish|powershell]",
		Short:     "Print a shell completion script",
		Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
		RunE: func(c *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return c.Root().GenBashCompletionV2(os.Stdout, true)
			case "zsh":
				return c.Root().GenZshCompletion(os.Stdout)
			case "fish":
				return c.Root().GenFishCompletion(os.Stdout, true)
			default:
				return c.Root().GenPowerShellCompletionWithDesc(os.Stdout)
			}
		},
	}
	return cmd
}

// ------------------------------------------------------------- install-cli

type installCLIResult struct {
	Type   string `json:"type"`
	Path   string `json:"path"`
	Linked bool   `json:"linked"`
	Skill  string `json:"skill,omitempty"`
}

// installCLICmd puts the running binary on PATH, which is what turns a plugin install into
// a standalone CLI an agent can call directly (docs/spec/05-cli.md §3.4).
func installCLICmd(g *globals) *cobra.Command {
	var dir string
	var force, withSkill bool
	cmd := &cobra.Command{
		Use:   "install-cli",
		Short: "Link this binary into a directory on PATH",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			id := "cli:install-cli"
			self, err := os.Executable()
			if err != nil {
				return failure(id, "io_error", err.Error(), ExitError, nil)
			}
			if resolved, err := filepath.EvalSymlinks(self); err == nil {
				self = resolved
			}
			target := dir
			if target == "" {
				target = defaultBinDir()
			}
			if err := os.MkdirAll(target, 0o755); err != nil {
				return failure(id, "io_error", err.Error(), ExitError, nil)
			}
			dest := filepath.Join(target, filepath.Base(self))

			if same, err := filepath.EvalSymlinks(dest); err == nil && same == self && !force {
				emit(os.Stdout, g, Envelope{ID: id,
					Result: installCLIResult{Type: "install_cli", Path: dest, Linked: true}})
				return nil
			}
			if _, err := os.Lstat(dest); err == nil {
				if !force {
					return failure(id, "usage",
						dest+" already exists; pass --force to replace it", ExitUsage, nil)
				}
				if err := os.Remove(dest); err != nil {
					return failure(id, "io_error", err.Error(), ExitError, nil)
				}
			}

			linked := true
			if err := link(self, dest); err != nil {
				// Windows without developer mode cannot symlink; a copy is the documented
				// fallback (docs/spec/08-agent-skill.md §6).
				if cerr := copyFile(self, dest); cerr != nil {
					return failure(id, "io_error",
						fmt.Sprintf("neither link nor copy worked: %v; %v", err, cerr), ExitError, nil)
				}
				linked = false
			}

			res := installCLIResult{Type: "install_cli", Path: dest, Linked: linked}
			if withSkill {
				p, err := installSkill()
				if err != nil {
					return failure(id, "io_error", err.Error(), ExitError, nil)
				}
				res.Skill = p
			}
			emit(os.Stdout, g, Envelope{ID: id, Result: res})
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "target directory (default: a platform bin directory on PATH)")
	cmd.Flags().BoolVar(&force, "force", false, "replace an existing entry")
	cmd.Flags().BoolVar(&withSkill, "with-skill", false, "also install the bundled Agent Skill")
	return cmd
}

func defaultBinDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	if runtime.GOOS == "windows" {
		if lad := os.Getenv("LocalAppData"); lad != "" {
			return filepath.Join(lad, "Microsoft", "WindowsApps")
		}
		return filepath.Join(home, "bin")
	}
	return filepath.Join(home, ".local", "bin")
}

func link(from, to string) error {
	if runtime.GOOS == "windows" {
		return os.Link(from, to)
	}
	return os.Symlink(from, to)
}

func copyFile(from, to string) error {
	b, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, b, 0o755)
}

// installSkill writes the whole bundle — SKILL.md plus references/ — into the canonical
// skills directory. `--skill` alone prints only the body, which would leave an agent with
// dangling reference links (docs/spec/08-agent-skill.md, open point 1).
func installSkill() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	root := filepath.Join(home, ".claude", "skills", skills.Name)
	tree, err := skills.FS()
	if err != nil {
		return "", err
	}
	err = fs.WalkDir(tree, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		dest := filepath.Join(root, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		b, err := fs.ReadFile(tree, p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dest, b, 0o644)
	})
	return root, err
}
