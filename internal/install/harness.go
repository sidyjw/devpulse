package install

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/sidiney/pm-mcp/internal/ui"
)

// Spec is the server entry the installer writes.
type Spec struct {
	Name    string
	Command string
	Args    []string
	Env     map[string]string
}

// Target is one place an app reads MCP servers from.
type Target struct {
	App   App
	Scope string
	Path  string   // config file; "" when the app only prints instructions
	Keys  []string // JSON path to the servers object, e.g. ["mcpServers"]
	Note  string   // shown next to the path
	// Custom is true when the user overrode the default path.
	Custom bool
}

// Key identifies the file location, so apps sharing a config are written once.
func (t Target) Key() string { return t.Path + "#" + strings.Join(t.Keys, "/") }

func (t Target) Label() string {
	s := t.App.Name()
	if t.Scope != "" {
		s += " (" + t.Scope + ")"
	}
	return s
}

// Opts are shared by Install and Uninstall.
type Opts struct {
	DryRun bool
	Out    io.Writer
}

// App is one application of a harness (Claude Code, Claude Desktop, …).
type App interface {
	ID() string
	Name() string
	Detect(e *Env) bool
	// Scopes lists the scopes the app supports (first = default); nil if none.
	Scopes() []string
	Target(e *Env, scope string) Target
	// Entry is the JSON value written for the server.
	Entry(s Spec) map[string]any
	Install(ctx context.Context, e *Env, t Target, s Spec, o Opts) error
	Uninstall(ctx context.Context, e *Env, t Target, name string, o Opts) (bool, error)
	NextSteps() string
}

// Harness is an AI client family (Claude, …).
type Harness interface {
	ID() string
	Name() string
	Apps() []App
}

// Harnesses is the registry, in the order shown to the user. To support a
// new harness, implement Harness/App in its own file and add it here; the
// generic one stays last.
func Harnesses() []Harness {
	return []Harness{claudeHarness{}, genericHarness{}}
}

func findHarness(id string) Harness {
	for _, h := range Harnesses() {
		if h.ID() == id {
			return h
		}
	}
	return nil
}

func harnessIDs() string {
	var ids []string
	for _, h := range Harnesses() {
		ids = append(ids, h.ID())
	}
	return strings.Join(ids, ", ")
}

// ---------- default JSON-file behavior shared by most apps ----------

func stdEntry(s Spec) map[string]any {
	m := map[string]any{"command": s.Command}
	if len(s.Args) > 0 {
		m["args"] = s.Args
	}
	if len(s.Env) > 0 {
		m["env"] = s.Env
	}
	return m
}

func fileInstall(app App, t Target, s Spec, o Opts) error {
	if t.Path == "" {
		fmt.Fprintln(o.Out, snippet(t.Keys, s.Name, app.Entry(s)))
		return nil
	}
	res, err := UpsertEntry(t.Path, t.Keys, s.Name, app.Entry(s), o.DryRun)
	if err != nil {
		return err
	}
	reportWrite(o, t.Path, res)
	return nil
}

func fileUninstall(t Target, name string, o Opts) (bool, error) {
	if t.Path == "" {
		return false, nil
	}
	res, err := RemoveEntry(t.Path, t.Keys, name, o.DryRun)
	if err != nil {
		return false, err
	}
	if res.Changed {
		reportWrite(o, t.Path, res)
	}
	return res.Changed, nil
}

// status prints one line that starts with a status marker (✓ ✗ ~ =).
func (o Opts) status(format string, args ...any) {
	fmt.Fprintln(o.Out, ui.For(o.Out).Mark(fmt.Sprintf(format, args...)))
}

func reportWrite(o Opts, path string, res WriteResult) {
	switch {
	case !res.Changed:
		o.status("  = %s já estava atualizado", path)
	case o.DryRun:
		o.status("  ~ %s seria alterado (dry-run)", path)
	case res.Backup != "":
		o.status("  ✓ %s atualizado %s", path, ui.For(o.Out).Dim("(backup: "+res.Backup+")"))
	default:
		o.status("  ✓ %s atualizado", path)
	}
}
