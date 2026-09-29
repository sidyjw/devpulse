package install

import (
	"context"
	"fmt"
	"strings"

	"github.com/sidiney/pm-mcp/internal/ui"
)

// genericHarness covers any client not in the registry. Without --config it
// prints the entry in the common formats; with --config it writes a JSON
// file under --key (default "mcpServers").
type genericHarness struct{}

func (genericHarness) ID() string   { return "generic" }
func (genericHarness) Name() string { return "Outra (genérica)" }
func (genericHarness) Apps() []App  { return []App{genericApp{}} }

type genericApp struct{}

func (genericApp) ID() string                  { return "generic" }
func (genericApp) Name() string                { return "Qualquer cliente MCP" }
func (genericApp) Detect(*Env) bool            { return false }
func (genericApp) Scopes() []string            { return nil }
func (genericApp) NextSteps() string           { return "Reinicie o cliente para ele carregar o servidor." }
func (genericApp) Entry(s Spec) map[string]any { return stdEntry(s) }

func (g genericApp) Target(*Env, string) Target {
	return Target{App: g, Keys: []string{"mcpServers"}, Note: "só mostra a configuração; use --config para gravar num arquivo JSON"}
}

func (g genericApp) Install(_ context.Context, _ *Env, t Target, s Spec, o Opts) error {
	if t.Path != "" {
		return fileInstall(g, t, s, o)
	}
	c := ui.For(o.Out)
	fmt.Fprintln(o.Out, "\n"+c.BoldCyan("JSON")+c.Dim(" (Claude, Cursor, Windsurf, Gemini CLI e a maioria dos clientes):"))
	fmt.Fprintln(o.Out, snippet(t.Keys, s.Name, g.Entry(s)))

	vs := stdEntry(s)
	vs["type"] = "stdio"
	fmt.Fprintln(o.Out, "\n"+c.BoldCyan("VS Code")+c.Dim(" (mcp.json):"))
	fmt.Fprintln(o.Out, snippet([]string{"servers"}, s.Name, vs))

	fmt.Fprintln(o.Out, "\n"+c.BoldCyan("TOML")+c.Dim(" (Codex, ~/.codex/config.toml):"))
	fmt.Fprintf(o.Out, "[mcp_servers.%s]\ncommand = %s\n", tomlKey(s.Name), tomlString(s.Command))
	if len(s.Args) > 0 {
		q := make([]string, len(s.Args))
		for i, a := range s.Args {
			q[i] = tomlString(a)
		}
		fmt.Fprintf(o.Out, "args = [%s]\n", strings.Join(q, ", "))
	}
	if len(s.Env) > 0 {
		fmt.Fprintf(o.Out, "\n[mcp_servers.%s.env]\n", tomlKey(s.Name))
		for _, k := range sortedKeys(s.Env) {
			fmt.Fprintf(o.Out, "%s = %s\n", k, tomlString(s.Env[k]))
		}
	}
	return nil
}

func (genericApp) Uninstall(_ context.Context, _ *Env, t Target, name string, o Opts) (bool, error) {
	if t.Path == "" {
		o.status("  ! remova a entrada %q da configuração do seu cliente", name)
		return false, nil
	}
	return fileUninstall(t, name, o)
}

func tomlString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`)
	return `"` + r.Replace(s) + `"`
}

func tomlKey(s string) string {
	for _, c := range s {
		if !(c == '-' || c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return tomlString(s)
		}
	}
	return s
}
