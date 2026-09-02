package skills

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The embedded skill must be byte-identical to the file in the repository, or
// `herdr-cron --skill` starts describing a binary that no longer exists. This test is the
// CI gate named by docs/spec/08-agent-skill.md §2.2.
func TestEmbeddedSkillMatchesTheRepository(t *testing.T) {
	embedded, err := SkillMD()
	if err != nil {
		t.Fatal(err)
	}
	onDisk, err := os.ReadFile(filepath.Join(Name, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(embedded, onDisk) {
		t.Fatal("the embedded SKILL.md differs from skills/herdr-cron/SKILL.md")
	}

	refs, err := References()
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) == 0 {
		t.Fatal("no reference files were embedded")
	}
	for _, rel := range refs {
		e, err := Read(rel)
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		d, err := os.ReadFile(filepath.Join(Name, rel))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		if !bytes.Equal(e, d) {
			t.Errorf("%s: embedded copy differs from the repository", rel)
		}
	}
}

// Only the six properties the Skills API accepts may appear; claude.ai rejects anything
// else with a hard error (docs/spec/08-agent-skill.md §3).
func TestFrontmatterUsesOnlyAllowedProperties(t *testing.T) {
	body, err := SkillMD()
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.HasPrefix(text, "---\n") {
		t.Fatal("SKILL.md does not start with YAML frontmatter")
	}
	end := strings.Index(text[4:], "\n---\n")
	if end < 0 {
		t.Fatal("the frontmatter is not terminated")
	}
	front := text[4 : 4+end]

	allowed := map[string]bool{
		"name": true, "description": true, "license": true,
		"allowed-tools": true, "metadata": true, "compatibility": true,
	}
	keyRe := regexp.MustCompile(`(?m)^([a-z][a-z0-9-]*):`)
	seen := map[string]bool{}
	for _, m := range keyRe.FindAllStringSubmatch(front, -1) {
		key := m[1]
		if !allowed[key] {
			t.Errorf("frontmatter key %q is not in the allowed set", key)
		}
		seen[key] = true
	}
	for _, required := range []string{"name", "description"} {
		if !seen[required] {
			t.Errorf("frontmatter is missing %q; three sources disagree on whether it is optional, so always write both", required)
		}
	}
}

// The description is the only part loaded up front, and it competes for a shared listing
// budget: 1024 chars is the validation cap, 1536 the listing budget
// (docs/spec/08-agent-skill.md §3.1).
func TestDescriptionFitsItsBudget(t *testing.T) {
	body, _ := SkillMD()
	line := ""
	for _, l := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(l, "description:") {
			line = l
			break
		}
	}
	if line == "" {
		t.Fatal("no description line")
	}
	desc := strings.TrimSpace(strings.TrimPrefix(line, "description:"))
	desc = strings.Trim(desc, `"`)
	if len(desc) > 1024 {
		t.Errorf("description is %d chars, over the 1024 validation cap", len(desc))
	}
	// Trigger keywords must come first: the listing truncates from the end.
	head := strings.ToLower(desc[:minInt(len(desc), 80)])
	for _, kw := range []string{"schedule"} {
		if !strings.Contains(head, kw) {
			t.Errorf("the first 80 chars of the description do not contain %q", kw)
		}
	}
}

// SKILL.md is loaded whole when the skill fires, so it stays under the documented cap.
func TestSkillBodyStaysShort(t *testing.T) {
	body, _ := SkillMD()
	if n := bytes.Count(body, []byte("\n")) + 1; n > 500 {
		t.Errorf("SKILL.md is %d lines, over the 500-line guidance", n)
	}
}

// Every reference must be named from SKILL.md or an agent will never read it
// (docs/spec/08-agent-skill.md §5).
func TestEveryReferenceIsLinkedFromTheBody(t *testing.T) {
	body, _ := SkillMD()
	refs, _ := References()
	for _, rel := range refs {
		if !bytes.Contains(body, []byte(rel)) {
			t.Errorf("%s is bundled but never named in SKILL.md", rel)
		}
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
