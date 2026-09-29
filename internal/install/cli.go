// Package install is the guided installer: it detects the OS and the AI
// harnesses (Claude Code, Claude Desktop, Cowork, …), asks which
// project-management tool and components to enable, and writes the MCP
// server entry into each chosen app's configuration.
package install

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/sidiney/devpulse/internal/provider"
	"github.com/sidiney/devpulse/internal/ui"
)

// Build describes the binary being installed.
type Build struct {
	Name, Version string
	Providers     []provider.Provider
}

// LegacyNames are the entry names used by earlier versions of the project,
// newest first: pm-mcp, and 7pace-mcp (entry "7pace") before it. Install
// migrates them; uninstall and detect find them too.
var LegacyNames = []string{"pm-mcp", "7pace"}

func isLegacy(name string) bool {
	for _, n := range LegacyNames {
		if n == name {
			return true
		}
	}
	return false
}

// IsCommand reports whether arg is an installer subcommand.
func IsCommand(arg string) bool {
	switch arg {
	case "install", "uninstall", "detect":
		return true
	}
	return false
}

type listFlag []string

func (l *listFlag) String() string { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error {
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			*l = append(*l, p)
		}
	}
	return nil
}

type setFlag map[string]string

func (s setFlag) String() string { return "" }
func (s setFlag) Set(v string) error {
	k, val, ok := strings.Cut(v, "=")
	k = strings.TrimSpace(k)
	if !ok || k == "" {
		return fmt.Errorf("use NOME=valor (recebido %q)", v)
	}
	s[k] = strings.TrimSpace(val)
	return nil
}

type options struct {
	harness, scope, config, key, name string
	apps, components                  listFlag
	providerID, binDir                string
	set                               setFlag
	noCopy, advanced, yes, dryRun     bool
	force, skipCheck                  bool
}

// Run executes an installer subcommand and returns the exit code.
func Run(args []string, b Build, stdin io.Reader, stdout, stderr io.Writer) int {
	return run(context.Background(), args, b, RealEnv(), stdin, stdout, stderr)
}

func run(ctx context.Context, args []string, b Build, e *Env, stdin io.Reader, stdout, stderr io.Writer) int {
	cmd := args[0]
	o := &options{set: setFlag{}}
	fs := flag.NewFlagSet(b.Name+" "+cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	if cmd != "detect" {
		fs.StringVar(&o.harness, "harness", "", "cliente de IA: "+harnessIDs())
		fs.Var(&o.apps, "app", "apps da harness, separados por vírgula (ex.: code,desktop,cowork)")
		fs.StringVar(&o.scope, "scope", "", "escopo do Claude Code: user, local ou project")
		fs.StringVar(&o.config, "config", "", "grava neste arquivo JSON em vez do caminho padrão")
		fs.StringVar(&o.key, "key", "", "caminho da chave dos servidores no JSON, separado por ponto (padrão: mcpServers)")
		fs.StringVar(&o.name, "name", b.Name, "nome da entrada do servidor")
		fs.BoolVar(&o.yes, "yes", false, "aceita os padrões sem perguntar (modo não interativo)")
		fs.BoolVar(&o.yes, "y", false, "o mesmo que --yes")
		fs.BoolVar(&o.dryRun, "dry-run", false, "mostra o que seria feito, sem gravar nada")
	}
	if cmd == "install" {
		fs.StringVar(&o.binDir, "bin-dir", "", "pasta para onde o executável é copiado")
		fs.BoolVar(&o.noCopy, "no-copy", false, "usa o executável onde ele está, sem copiar")
		fs.StringVar(&o.providerID, "provider", "", "ferramenta de gestão (ex.: azuredevops)")
		fs.Var(&o.components, "components", "componentes, separados por vírgula (ex.: boards,sevenpace)")
		fs.Var(o.set, "set", "define uma variável: --set NOME=valor (pode repetir)")
		fs.BoolVar(&o.advanced, "advanced", false, "pergunta também as opções avançadas")
		fs.BoolVar(&o.force, "force", false, "substitui entradas existentes sem perguntar")
		fs.BoolVar(&o.skipCheck, "skip-check", false, "não roda o -check antes de gravar")
	}
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	ec := ui.For(stderr)
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "%s argumento inesperado: %s\n", ec.BoldRed("erro:"), fs.Arg(0))
		return 2
	}

	w := &wizard{ctx: ctx, b: b, e: e, o: o, p: NewPrompter(stdin, stdout, o.yes), out: stdout, c: ui.For(stdout)}
	var err error
	switch cmd {
	case "install":
		err = w.install()
	case "uninstall":
		err = w.uninstall()
	case "detect":
		w.detect()
	}
	if err != nil {
		if errors.Is(err, errAborted) {
			fmt.Fprintln(stdout, w.c.Yellow("Nada foi alterado."))
			return 1
		}
		fmt.Fprintln(stderr, ec.BoldRed("erro:"), ec.Red(err.Error()))
		return 1
	}
	return 0
}
