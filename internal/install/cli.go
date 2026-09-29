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

	"github.com/sidiney/pm-mcp/internal/provider"
)

// Build describes the binary being installed.
type Build struct {
	Name, Version string
	Providers     []provider.Provider
}

// LegacyName is the entry name used by 7pace-mcp, migrated on install.
const LegacyName = "7pace"

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
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "argumento inesperado: %s\n", fs.Arg(0))
		return 2
	}

	w := &wizard{ctx: ctx, b: b, e: e, o: o, p: NewPrompter(stdin, stdout, o.yes), out: stdout}
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
			fmt.Fprintln(stdout, "Nada foi alterado.")
			return 1
		}
		fmt.Fprintln(stderr, "erro:", err)
		return 1
	}
	return 0
}
