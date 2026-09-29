// Command pm-mcp is a local MCP server (stdio) for project-management tools.
// Each tool is a provider with independent components (today: Azure DevOps
// with Boards and 7pace Timetracker). Standard library only; no telemetry;
// talks only to the hosts you configure.
//
//	pm-mcp                 serve MCP over stdio
//	pm-mcp -check          validate the configuration and test connections
//	pm-mcp install         guided installation into an AI harness
//	pm-mcp uninstall       remove it from a harness
//	pm-mcp detect          show the OS, harnesses and existing installations
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/sidiney/pm-mcp/internal/httpx"
	"github.com/sidiney/pm-mcp/internal/install"
	"github.com/sidiney/pm-mcp/internal/mcp"
	"github.com/sidiney/pm-mcp/internal/provider"
	"github.com/sidiney/pm-mcp/internal/providers"
	"github.com/sidiney/pm-mcp/internal/settings"
)

const (
	name    = "pm-mcp"
	version = "2.0.0"
)

const instructions = `Servidor para trabalhar com ferramentas de gestão de projetos.
Regras:
- Antes de qualquer escrita (lançar horas, criar ou alterar itens, comentar), mostre ao usuário
  exatamente o que será feito e peça confirmação.
- Nunca peça ao usuário tokens ou PATs no chat: eles são configurados fora da conversa.`

func main() {
	if len(os.Args) > 1 && install.IsCommand(os.Args[1]) {
		os.Exit(install.Run(os.Args[1:], install.Build{Name: name, Version: version, Providers: providers.All()}, os.Stdin, os.Stdout, os.Stderr))
	}

	showVersion := flag.Bool("version", false, "mostra a versão e sai")
	check := flag.Bool("check", false, "valida a configuração, testa as conexões e sai")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "uso: %s [-check | -version]\n       %s install | uninstall | detect [flags]   (use -h em cada um)\n", name, name)
		flag.PrintDefaults()
	}
	flag.Parse()

	if *showVersion {
		fmt.Println(name, version)
		return
	}

	httpx.UserAgent = name + "/" + version
	timeout, err := settings.HTTPTimeout(os.Getenv)
	if err != nil {
		mcp.Logf("configuração inválida: %v", err)
		os.Exit(2)
	}
	active, instr, err := provider.Activate(providers.All(), os.Getenv, httpx.NewHTTPClient(timeout), instructions)
	if err != nil {
		mcp.Logf("configuração inválida: %v", err)
		os.Exit(2)
	}

	if *check {
		os.Exit(runCheck(active))
	}

	srv := mcp.NewServer(name, version, instr)
	for _, a := range active {
		a.Instance.Register(srv)
	}
	mcp.Logf("v%s pronto (stdio). Tools: %s", version, strings.Join(srv.ToolNames(), ", "))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := srv.Serve(ctx, os.Stdin, os.Stdout); err != nil {
		mcp.Logf("erro: %v", err)
		os.Exit(1)
	}
}

func runCheck(active []provider.Active) int {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	code := 0
	for _, a := range active {
		if err := a.Instance.Check(ctx, os.Stdout); err != nil {
			code = 1
		}
	}
	return code
}
