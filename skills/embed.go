// Package skills embeds the Agent Skill that ships with the binary.
//
// The embed is what makes drift impossible: `herdr-cron --skill` prints the same bytes the
// repository holds, the way `herdr --skill` does (docs/spec/08-agent-skill.md §2).
package skills

import (
	"embed"
	"io"
	"io/fs"
	"path"
)

//go:embed herdr-cron/SKILL.md herdr-cron/references/*.md
var files embed.FS

// Name is the skill's directory name, and the name in its frontmatter.
const Name = "herdr-cron"

// SkillMD returns the skill body.
func SkillMD() ([]byte, error) {
	return files.ReadFile(path.Join(Name, "SKILL.md"))
}

// References lists the bundled reference files by their path relative to the skill
// directory. They are free until an agent reads one, which is why the CLI reference lives
// there rather than in SKILL.md.
func References() ([]string, error) {
	entries, err := fs.ReadDir(files, path.Join(Name, "references"))
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, path.Join("references", e.Name()))
	}
	return out, nil
}

// Read returns a bundled file by its skill-relative path.
func Read(rel string) ([]byte, error) {
	return files.ReadFile(path.Join(Name, rel))
}

// Print writes SKILL.md to w.
func Print(w io.Writer) error {
	b, err := SkillMD()
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// FS exposes the embedded tree, rooted at the skill directory, so an installer can copy
// the whole bundle.
func FS() (fs.FS, error) { return fs.Sub(files, Name) }
