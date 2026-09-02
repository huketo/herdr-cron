package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"gopkg.in/yaml.v3"
)

// Edit describes a job add or update. A nil field means "leave alone" on update and
// "use the default" on add.
type Edit struct {
	ID          string
	Name        *string
	Description *string
	Schedule    *string // one flag, disambiguated by shape
	Timezone    *string
	Catchup     *string
	Concurrency *string
	Cwd         *string
	Env         map[string]string
	Tags        []string
	Timeout     *string
	Enabled     *bool

	Command *string // kind: shell
	Prompt  *string // kind: agent

	AgentKind  *string
	Session    *string
	NoOpMarker *string

	MaxAttempts   *int
	MaxRunsPerDay *int
}

// ErrJobExists and friends let the CLI map a failure onto a stable error code.
var (
	ErrJobExists   = errors.New("job already exists")
	ErrJobNotFound = errors.New("job not found")
)

// Apply mutates jobs.yaml under its lock, preserving comments and key order, and
// validating the result before anything is renamed into place
// (docs/spec/04-storage.md §3).
func Apply(path string, mutate func(jobs *yaml.Node) error) (*Loaded, []Issue, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, nil, err
	}
	lock := flock.New(path + ".lock")
	if err := lock.Lock(); err != nil {
		return nil, nil, err
	}
	defer func() { _ = lock.Unlock() }()

	doc, err := readDoc(path)
	if err != nil {
		return nil, nil, err
	}
	root := doc.Content[0]
	jobs, err := jobsNode(root)
	if err != nil {
		return nil, nil, err
	}
	if err := mutate(jobs); err != nil {
		return nil, nil, err
	}

	rendered, err := render(doc)
	if err != nil {
		return nil, nil, err
	}

	// Validate the rendered bytes, not the node tree: what would be written is what is checked.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".jobs.yaml.*")
	if err != nil {
		return nil, nil, err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(rendered); err != nil {
		_ = tmp.Close()
		return nil, nil, err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return nil, nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, nil, err
	}

	loaded, issues := Load(tmpName)
	if len(issues) > 0 {
		return nil, issues, nil // nothing is written
	}
	loaded.Path = path

	if err := os.Rename(tmpName, path); err != nil {
		time.Sleep(50 * time.Millisecond)
		if err := os.Rename(tmpName, path); err != nil {
			return nil, nil, err
		}
	}
	return loaded, nil, nil
}

func readDoc(path string) (*yaml.Node, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		b = []byte("version: 1\njobs: []\n")
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("%s is not parseable YAML: %w", path, err)
	}
	if len(doc.Content) == 0 {
		if err := yaml.Unmarshal([]byte("version: 1\njobs: []\n"), &doc); err != nil {
			return nil, err
		}
	}
	return &doc, nil
}

func render(doc *yaml.Node) ([]byte, error) {
	var sb strings.Builder
	enc := yaml.NewEncoder(&sb)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return []byte(sb.String()), nil
}

func jobsNode(root *yaml.Node) (*yaml.Node, error) {
	if root.Kind != yaml.MappingNode {
		return nil, errors.New("jobs.yaml must be a mapping at the top level")
	}
	if n := mapGet(root, "jobs"); n != nil {
		if n.Kind != yaml.SequenceNode {
			// An empty `jobs:` decodes as a null scalar; turn it into a sequence.
			n.Kind = yaml.SequenceNode
			n.Tag = "!!seq"
			n.Value = ""
			n.Content = nil
		}
		return n, nil
	}
	key := scalar("jobs")
	val := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	root.Content = append(root.Content, key, val)
	if mapGet(root, "version") == nil {
		root.Content = append([]*yaml.Node{scalar("version"), intScalar(1)}, root.Content...)
	}
	return val, nil
}

// AddJob appends a new job. It fails if the id is taken.
func AddJob(e Edit) func(*yaml.Node) error {
	return func(jobs *yaml.Node) error {
		if findJob(jobs, e.ID) != nil {
			return fmt.Errorf("%w: %s", ErrJobExists, e.ID)
		}
		node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setScalar(node, "id", e.ID)
		if err := applyEdit(node, e); err != nil {
			return err
		}
		jobs.Content = append(jobs.Content, node)
		return nil
	}
}

// UpdateJob mutates an existing job in place, touching only the fields the caller set.
func UpdateJob(e Edit) func(*yaml.Node) error {
	return func(jobs *yaml.Node) error {
		node := findJob(jobs, e.ID)
		if node == nil {
			return fmt.Errorf("%w: %s", ErrJobNotFound, e.ID)
		}
		return applyEdit(node, e)
	}
}

// RemoveJob deletes a job.
func RemoveJob(id string) func(*yaml.Node) error {
	return func(jobs *yaml.Node) error {
		for i, n := range jobs.Content {
			if scalarOf(mapGet(n, "id")) == id {
				jobs.Content = append(jobs.Content[:i], jobs.Content[i+1:]...)
				return nil
			}
		}
		return fmt.Errorf("%w: %s", ErrJobNotFound, id)
	}
}

func applyEdit(node *yaml.Node, e Edit) error {
	setIfString(node, "name", e.Name)
	setIfString(node, "description", e.Description)
	if e.Enabled != nil {
		setBool(node, "enabled", *e.Enabled)
	}
	if e.Tags != nil {
		setStringSlice(node, "tags", e.Tags)
	}

	if e.Schedule != nil {
		sched := mapEnsure(node, "schedule")
		form, value, err := scheduleForm(*e.Schedule)
		if err != nil {
			return err
		}
		for _, k := range []string{"cron", "every", "at"} {
			if k != form {
				mapDelete(sched, k)
			}
		}
		setScalar(sched, form, value)
		setIfString(sched, "timezone", e.Timezone)
		setIfString(sched, "catchup", e.Catchup)
	} else if e.Timezone != nil || e.Catchup != nil {
		sched := mapEnsure(node, "schedule")
		setIfString(sched, "timezone", e.Timezone)
		setIfString(sched, "catchup", e.Catchup)
	}

	switch {
	case e.Command != nil:
		setScalar(node, "kind", "shell")
		mapDelete(node, "agent")
		setScalar(mapEnsure(node, "shell"), "command", *e.Command)
	case e.Prompt != nil:
		setScalar(node, "kind", "agent")
		mapDelete(node, "shell")
		agent := mapEnsure(node, "agent")
		setScalar(agent, "prompt", *e.Prompt)
		setIfString(agent, "agent_kind", e.AgentKind)
		setIfString(agent, "session", e.Session)
		setIfString(agent, "no_op_marker", e.NoOpMarker)
	}

	setIfString(node, "cwd", e.Cwd)
	setIfString(node, "timeout", e.Timeout)
	setIfString(node, "concurrency", e.Concurrency)
	if e.Env != nil {
		env := mapEnsure(node, "env")
		keys := make([]string, 0, len(e.Env))
		for k := range e.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			setScalar(env, k, e.Env[k])
		}
	}
	if e.MaxAttempts != nil {
		setInt(mapEnsure(node, "retry"), "max_attempts", *e.MaxAttempts)
	}
	if e.MaxRunsPerDay != nil {
		setInt(mapEnsure(node, "limits"), "max_runs_per_day", *e.MaxRunsPerDay)
	}
	return nil
}

// scheduleForm disambiguates the single --schedule flag by shape
// (docs/spec/05-cli.md §3.1).
func scheduleForm(expr string) (form, value string, err error) {
	switch {
	case strings.HasPrefix(expr, "@"):
		return "cron", expr, nil
	case isRFC3339(expr):
		return "at", expr, nil
	case !strings.ContainsAny(expr, " \t"):
		if _, err := time.ParseDuration(expr); err != nil {
			return "", "", fmt.Errorf("%q is neither a duration nor a cron expression", expr)
		}
		return "every", expr, nil
	default:
		return "cron", expr, nil
	}
}

func isRFC3339(v string) bool {
	_, err := time.Parse(time.RFC3339, v)
	return err == nil
}

// ---------------------------------------------------------------- node helpers

func scalar(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
}

func intScalar(v int) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: fmt.Sprint(v)}
}

func scalarOf(n *yaml.Node) string {
	if n == nil {
		return ""
	}
	return n.Value
}

func mapGet(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func mapEnsure(m *yaml.Node, key string) *yaml.Node {
	if n := mapGet(m, key); n != nil {
		if n.Kind != yaml.MappingNode {
			n.Kind = yaml.MappingNode
			n.Tag = "!!map"
			n.Value = ""
			n.Content = nil
		}
		return n
	}
	v := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	m.Content = append(m.Content, scalar(key), v)
	return v
}

func mapDelete(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}

// setScalar replaces a value while keeping the key node, and with it any comment
// attached to that key.
func setScalar(m *yaml.Node, key, value string) {
	style := yaml.Style(0)
	if strings.Contains(value, "\n") {
		style = yaml.LiteralStyle
	}
	if n := mapGet(m, key); n != nil {
		n.Kind = yaml.ScalarNode
		n.Tag = "!!str"
		n.Value = value
		n.Style = style
		n.Content = nil
		return
	}
	v := scalar(value)
	v.Style = style
	m.Content = append(m.Content, scalar(key), v)
}

func setIfString(m *yaml.Node, key string, v *string) {
	if v != nil {
		setScalar(m, key, *v)
	}
}

func setBool(m *yaml.Node, key string, v bool) {
	node := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: fmt.Sprint(v)}
	if n := mapGet(m, key); n != nil {
		*n = *node
		return
	}
	m.Content = append(m.Content, scalar(key), node)
}

func setInt(m *yaml.Node, key string, v int) {
	node := intScalar(v)
	if n := mapGet(m, key); n != nil {
		*n = *node
		return
	}
	m.Content = append(m.Content, scalar(key), node)
}

func setStringSlice(m *yaml.Node, key string, vs []string) {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
	for _, v := range vs {
		seq.Content = append(seq.Content, scalar(v))
	}
	if n := mapGet(m, key); n != nil {
		*n = *seq
		return
	}
	m.Content = append(m.Content, scalar(key), seq)
}

func findJob(jobs *yaml.Node, id string) *yaml.Node {
	for _, n := range jobs.Content {
		if scalarOf(mapGet(n, "id")) == id {
			return n
		}
	}
	return nil
}
