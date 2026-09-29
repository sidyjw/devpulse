package install

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// ---------- Claude ----------

type claudeHarness struct{}

func (claudeHarness) ID() string   { return "claude" }
func (claudeHarness) Name() string { return "Claude" }
func (claudeHarness) Apps() []App {
	return []App{claudeCode{}, claudeDesktop{}, claudeCowork{}}
}

// ---------- Claude Code ----------

type claudeCode struct{}

func (claudeCode) ID() string       { return "code" }
func (claudeCode) Name() string     { return "Claude Code" }
func (claudeCode) Scopes() []string { return []string{"user", "local", "project"} }

func (claudeCode) Detect(e *Env) bool {
	return claudeCLI(e) != "" || exists(filepath.Join(e.Home, ".claude.json")) || isDir(filepath.Join(e.Home, ".claude"))
}

func (c claudeCode) Target(e *Env, scope string) Target {
	t := Target{App: c, Scope: scope, Path: filepath.Join(e.Home, ".claude.json"), Keys: []string{"mcpServers"}}
	switch scope {
	case "local":
		// Claude Code keys projects by their absolute path with forward slashes.
		t.Keys = []string{"projects", filepath.ToSlash(e.Cwd), "mcpServers"}
		t.Note = "só neste diretório: " + e.Cwd
	case "project":
		t.Path = filepath.Join(e.Cwd, ".mcp.json")
		t.Note = "compartilhado com o repositório"
	default:
		t.Scope = "user"
		t.Note = "todos os projetos"
	}
	return t
}

func (claudeCode) Entry(s Spec) map[string]any {
	m := stdEntry(s)
	m["type"] = "stdio"
	return m
}

// claudeCLI returns the path of the `claude` executable when it can be run
// safely with a JSON argument. .cmd/.bat shims (npm installs on Windows) go
// through cmd.exe, which mangles quotes, so those fall back to file editing.
func claudeCLI(e *Env) string {
	if e.LookPath == nil {
		return ""
	}
	p, err := e.LookPath("claude")
	if err != nil {
		return ""
	}
	switch strings.ToLower(filepath.Ext(p)) {
	case ".cmd", ".bat", ".ps1":
		return ""
	}
	return p
}

func (c claudeCode) Install(ctx context.Context, e *Env, t Target, s Spec, o Opts) error {
	cli := claudeCLI(e)
	if cli == "" || t.Custom {
		return fileInstall(c, t, s, o)
	}
	entries, _ := ReadEntries(t.Path, t.Keys)
	if _, ok := entries[s.Name]; ok {
		if o.DryRun {
			fmt.Fprintf(o.Out, "  ~ claude mcp remove %s -s %s (dry-run)\n", s.Name, t.Scope)
		} else if _, err := e.Run(ctx, Cmd{Name: cli, Args: []string{"mcp", "remove", s.Name, "-s", t.Scope}, Dir: e.Cwd}); err != nil {
			return fmt.Errorf("claude mcp remove: %w", err)
		}
	}
	js, err := json.Marshal(c.Entry(s))
	if err != nil {
		return err
	}
	if o.DryRun {
		fmt.Fprintf(o.Out, "  ~ claude mcp add-json %s <json> -s %s (dry-run)\n", s.Name, t.Scope)
		return nil
	}
	if _, err := e.Run(ctx, Cmd{Name: cli, Args: []string{"mcp", "add-json", s.Name, string(js), "-s", t.Scope}, Dir: e.Cwd}); err != nil {
		return fmt.Errorf("claude mcp add-json: %w", err)
	}
	fmt.Fprintf(o.Out, "  ✓ registrado com `claude mcp add-json` (escopo %s)\n", t.Scope)
	return nil
}

func (c claudeCode) Uninstall(ctx context.Context, e *Env, t Target, name string, o Opts) (bool, error) {
	cli := claudeCLI(e)
	if cli == "" || t.Custom {
		return fileUninstall(t, name, o)
	}
	entries, _ := ReadEntries(t.Path, t.Keys)
	if _, ok := entries[name]; !ok {
		return false, nil
	}
	if o.DryRun {
		fmt.Fprintf(o.Out, "  ~ claude mcp remove %s -s %s (dry-run)\n", name, t.Scope)
		return true, nil
	}
	if _, err := e.Run(ctx, Cmd{Name: cli, Args: []string{"mcp", "remove", name, "-s", t.Scope}, Dir: e.Cwd}); err != nil {
		return false, fmt.Errorf("claude mcp remove: %w", err)
	}
	fmt.Fprintf(o.Out, "  ✓ removido com `claude mcp remove` (escopo %s)\n", t.Scope)
	return true, nil
}

func (claudeCode) NextSteps() string {
	return "Abra uma nova sessão do Claude Code e confira com `claude mcp list` (ou /mcp dentro da sessão)."
}

// ---------- Claude Desktop ----------

type claudeDesktop struct{}

func (claudeDesktop) ID() string       { return "desktop" }
func (claudeDesktop) Name() string     { return "Claude Desktop" }
func (claudeDesktop) Scopes() []string { return nil }

// desktopConfigDir returns the directory Claude Desktop reads its config
// from, and a note. On Windows the MSIX (Store) build reads from its
// virtualized package folder, not from %APPDATA%.
func desktopConfigDir(e *Env) (string, string) {
	switch e.GOOS {
	case "windows":
		if e.LocalAppData != "" {
			if ms, _ := filepath.Glob(filepath.Join(e.LocalAppData, "Packages", "Claude_*", "LocalCache", "Roaming", "Claude")); len(ms) > 0 {
				sort.Strings(ms)
				return ms[0], "instalação MSIX/Microsoft Store"
			}
		}
		return filepath.Join(e.AppData, "Claude"), ""
	case "darwin":
		return filepath.Join(e.Home, "Library", "Application Support", "Claude"), ""
	default:
		return filepath.Join(e.XDGConfig, "Claude"), "Linux não tem build oficial do Claude Desktop"
	}
}

func (claudeDesktop) Detect(e *Env) bool {
	dir, _ := desktopConfigDir(e)
	return isDir(dir)
}

func (d claudeDesktop) Target(e *Env, _ string) Target {
	dir, note := desktopConfigDir(e)
	return Target{App: d, Path: filepath.Join(dir, "claude_desktop_config.json"), Keys: []string{"mcpServers"}, Note: note}
}

func (claudeDesktop) Entry(s Spec) map[string]any { return stdEntry(s) }

func (d claudeDesktop) Install(_ context.Context, _ *Env, t Target, s Spec, o Opts) error {
	return fileInstall(d, t, s, o)
}

func (claudeDesktop) Uninstall(_ context.Context, _ *Env, t Target, name string, o Opts) (bool, error) {
	return fileUninstall(t, name, o)
}

func (claudeDesktop) NextSteps() string {
	return "Feche o Claude Desktop por completo (inclusive pelo ícone da bandeja/menu) e abra de novo."
}

// ---------- Claude Cowork ----------

// Cowork runs inside Claude Desktop and gets the local servers of
// claude_desktop_config.json through Desktop's bridge, so it shares
// Desktop's target (the wizard writes it only once).
type claudeCowork struct{ claudeDesktop }

func (claudeCowork) ID() string   { return "cowork" }
func (claudeCowork) Name() string { return "Claude Cowork" }

func (c claudeCowork) Detect(e *Env) bool {
	return (e.GOOS == "windows" || e.GOOS == "darwin") && c.claudeDesktop.Detect(e)
}

func (c claudeCowork) Target(e *Env, _ string) Target {
	t := c.claudeDesktop.Target(e, "")
	t.App = c
	t.Note = strings.TrimPrefix(t.Note+"; usa a configuração do Claude Desktop", "; ")
	return t
}

func (c claudeCowork) Install(_ context.Context, _ *Env, t Target, s Spec, o Opts) error {
	return fileInstall(c, t, s, o)
}

func (claudeCowork) NextSteps() string {
	return "Reinicie o Claude Desktop; no Cowork os servidores locais chegam pela ponte do Desktop."
}
