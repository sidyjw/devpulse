// Package azuredevops is the Azure DevOps provider: the Boards component
// (sprints, board, work items) and the 7pace Timetracker component
// (logging and fixing hours), which is an Azure DevOps extension.
package azuredevops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/sidiney/pm-mcp/internal/mcp"
	"github.com/sidiney/pm-mcp/internal/provider"
	"github.com/sidiney/pm-mcp/internal/settings"
	"github.com/sidiney/pm-mcp/internal/ui"
)

const (
	ProviderID  = "azuredevops"
	BoardsID    = "boards"
	SevenPaceID = "sevenpace"
	boardsKey   = ProviderID + "." + BoardsID
)

// Provider returns the Azure DevOps provider. Boards comes first so the
// 7pace component can use it to show work item titles.
func Provider() provider.Provider {
	return provider.Provider{
		ID:         ProviderID,
		Name:       "Azure DevOps",
		Components: []provider.Component{boardsComponent(), sevenPaceComponent()},
	}
}

// secretDefault suggests ~/.pm-mcp/<name>, or the legacy ~/.7pace/<legacy>
// when that file already exists.
func secretDefault(name, legacy string) func(string) string {
	return func(home string) string {
		if home == "" {
			return ""
		}
		old := filepath.Join(home, ".7pace", legacy)
		if fi, err := os.Stat(old); err == nil && fi.Mode().IsRegular() {
			return old
		}
		return filepath.Join(home, ".pm-mcp", name)
	}
}

// ---------- Boards ----------

type boardsConfig struct {
	OrgURL, PAT, Project, Team string
	ReadOnly                   bool
}

func loadBoardsConfig(getenv func(string) string) (*boardsConfig, error) {
	org := strings.TrimSpace(getenv("AZURE_DEVOPS_ORG_URL"))
	root, err := settings.ValidateHTTPSURL(org)
	if err != nil {
		return nil, fmt.Errorf("AZURE_DEVOPS_ORG_URL: %w", err)
	}
	pat, err := settings.SecretFromEnv(getenv, "AZURE_DEVOPS_PAT")
	if err != nil {
		return nil, err
	}
	if pat == "" {
		return nil, errors.New("AZURE_DEVOPS_ORG_URL definido sem AZURE_DEVOPS_PAT / AZURE_DEVOPS_PAT_FILE")
	}
	return &boardsConfig{
		OrgURL:   root,
		PAT:      pat,
		Project:  strings.TrimSpace(getenv("AZURE_DEVOPS_PROJECT")),
		Team:     strings.TrimSpace(getenv("AZURE_DEVOPS_TEAM")),
		ReadOnly: settings.EnvBool(getenv("AZURE_DEVOPS_READ_ONLY")),
	}, nil
}

func boardsComponent() provider.Component {
	return provider.Component{
		ID:          BoardsID,
		Name:        "Boards",
		Description: "Sprints, quadro, épicos, features, user stories, tasks e bugs",
		Settings: []provider.Setting{
			{Env: "AZURE_DEVOPS_ORG_URL", Label: "URL da organização", Help: "ex.: https://dev.azure.com/minhaorg",
				Kind: provider.URL, Required: true, Validate: settings.CheckHTTPSURL},
			{Env: "AZURE_DEVOPS_PAT_FILE", Label: "Arquivo com o PAT", Help: "escopos: Work Items (Read & write) e Project and Team (Read)",
				Kind: provider.SecretFile, Required: true, SecretAlt: "AZURE_DEVOPS_PAT", Default: secretDefault("azdo-pat", "azdo-pat"),
				Guide: &provider.Guide{
					Title: "Como gerar o PAT (Personal Access Token) do Azure DevOps",
					Steps: []string{
						"Abra {AZURE_DEVOPS_ORG_URL|https://dev.azure.com/<sua-org>}/_usersSettings/tokens (ou, no Azure DevOps: ícone de usuário no canto superior direito → Personal access tokens).",
						"Clique em New Token, dê um nome (ex.: pm-mcp) e escolha a organização e a validade.",
						"Em Scopes, escolha Custom defined e marque: Work Items → Read & write; Project and Team → Read (use Show all scopes se algum não aparecer). Para uso somente leitura, Work Items → Read basta.",
						"Clique em Create e copie o token: ele só é mostrado uma vez.",
					},
				}},
			{Env: "AZURE_DEVOPS_PROJECT", Label: "Projeto padrão", Help: "opcional; evita repetir o projeto em toda pergunta"},
			{Env: "AZURE_DEVOPS_TEAM", Label: "Time padrão", Help: "opcional; se vazio, usa o time padrão do projeto", Advanced: true},
			{Env: "AZURE_DEVOPS_READ_ONLY", Label: "Somente leitura?", Help: "esconde as tools que criam ou alteram work items",
				Kind: provider.Bool, Advanced: true, Validate: settings.CheckBool},
		},
		Instructions: "- Para achar o work item certo, use query_work_items (ex.: assignedTo=\"me\", sprint=\"current\").",
		Enabled: func(getenv func(string) string) bool {
			return strings.TrimSpace(getenv("AZURE_DEVOPS_ORG_URL")) != ""
		},
		Build: func(bc *provider.BuildContext) (provider.Instance, error) {
			cfg, err := loadBoardsConfig(bc.Getenv)
			if err != nil {
				return nil, err
			}
			return &boardsInstance{cfg: cfg, az: NewAzDO(cfg.OrgURL, cfg.PAT, cfg.Project, cfg.Team, bc.HTTP)}, nil
		},
	}
}

type boardsInstance struct {
	cfg *boardsConfig
	az  *AzDO
}

func (b *boardsInstance) Register(s *mcp.Server) { registerAzDOTools(s, b.az, b.cfg) }

func (b *boardsInstance) Check(ctx context.Context, w io.Writer) error {
	c := ui.For(w)
	line := func(format string, args ...any) { fmt.Fprintln(w, c.Mark(fmt.Sprintf(format, args...))) }
	fmt.Fprintln(w, c.BoldCyan("Azure DevOps:"), b.cfg.OrgURL)
	defer func() { fmt.Fprintln(w, c.Dim(fmt.Sprintf("  somente leitura: %v", b.cfg.ReadOnly))) }()
	me, err := b.az.Me(ctx)
	if err != nil {
		line("  ✗ %v", err)
		return err
	}
	line("  ✓ conectado como %s (%s)", c.Bold(me.DisplayName), me.Account)
	var failed error
	if ps, err := b.az.Projects(ctx); err != nil {
		line("  ✗ listar projetos: %v (o PAT precisa de Project and Team: Read)", err)
		failed = err
	} else {
		line("  ✓ %d projetos visíveis", len(ps))
	}
	if b.cfg.Project != "" {
		if sps, _, team, err := b.az.Sprints(ctx, "", "", "current"); err != nil {
			line("  ✗ sprints: %v", err)
			failed = err
		} else if len(sps) > 0 {
			line("  ✓ time %q, sprint atual: %s (%s a %s)", team, sps[0].Name, sps[0].StartDate, sps[0].FinishDate)
		} else {
			line("  ✓ time %q (sem sprint atual)", team)
		}
	}
	return failed
}

// ---------- 7pace Timetracker ----------

type sevenPaceConfig struct {
	APIRoot, Token         string
	ReadOnly, EnableDelete bool
}

func sevenPaceEnabled(getenv func(string) string) bool {
	for _, n := range []string{"SEVENPACE_BASE_URL", "SEVENPACE_ORGANIZATION", "SEVENPACE_TOKEN", "SEVENPACE_TOKEN_FILE"} {
		if strings.TrimSpace(getenv(n)) != "" {
			return true
		}
	}
	return false
}

func loadSevenPaceConfig(getenv func(string) string) (*sevenPaceConfig, error) {
	cfg := &sevenPaceConfig{}
	base := strings.TrimSpace(getenv("SEVENPACE_BASE_URL"))
	org := strings.TrimSpace(getenv("SEVENPACE_ORGANIZATION"))
	tok, err := settings.SecretFromEnv(getenv, "SEVENPACE_TOKEN")
	if err != nil {
		return nil, err
	}
	if base != "" {
		root, err := settings.ValidateHTTPSURL(base)
		if err != nil {
			return nil, fmt.Errorf("SEVENPACE_BASE_URL: %w", err)
		}
		cfg.APIRoot = root
	} else {
		if org == "" {
			return nil, errors.New("defina SEVENPACE_ORGANIZATION (ou SEVENPACE_BASE_URL)")
		}
		if err := settings.CheckOrgName(org); err != nil {
			return nil, fmt.Errorf("SEVENPACE_ORGANIZATION: %w", err)
		}
		cfg.APIRoot = "https://" + org + ".timehub.7pace.com/api"
	}
	if tok == "" {
		return nil, errors.New("defina SEVENPACE_TOKEN ou SEVENPACE_TOKEN_FILE")
	}
	cfg.Token = tok
	cfg.ReadOnly = settings.EnvBool(getenv("SEVENPACE_READ_ONLY"))
	cfg.EnableDelete = settings.EnvBool(getenv("SEVENPACE_ENABLE_DELETE"))
	return cfg, nil
}

func sevenPaceComponent() provider.Component {
	return provider.Component{
		ID:          SevenPaceID,
		Name:        "7pace Timetracker",
		Description: "Lançar, corrigir e resumir horas",
		Settings: []provider.Setting{
			{Env: "SEVENPACE_ORGANIZATION", Label: "Organização do 7pace", Help: "o prefixo de https://<org>.timehub.7pace.com",
				Required: true, Validate: settings.CheckOrgName},
			{Env: "SEVENPACE_TOKEN_FILE", Label: "Arquivo com o token do 7pace", Help: "gere em 7pace → Settings → API & Reporting",
				Kind: provider.SecretFile, Required: true, SecretAlt: "SEVENPACE_TOKEN", Default: secretDefault("7pace-token", "token"),
				Guide: &provider.Guide{
					Title: "Como gerar o token da API do 7pace Timetracker",
					Steps: []string{
						"Abra o 7pace em https://{SEVENPACE_ORGANIZATION|<org>}.timehub.7pace.com (ou pela aba Timetracker dentro do Azure DevOps).",
						"Vá em Settings (ícone de engrenagem) → API & Reporting.",
						"Crie um novo token (Create New Token) com um nome como pm-mcp.",
						"Copie o token: ele só é mostrado uma vez. Se a opção não aparecer, peça ao administrador do 7pace acesso à API.",
					},
				}},
			{Env: "SEVENPACE_BASE_URL", Label: "URL da API (substitui a organização)", Help: "só para ambientes fora do padrão; precisa ser https",
				Kind: provider.URL, Advanced: true, Validate: settings.CheckHTTPSURL},
			{Env: "SEVENPACE_READ_ONLY", Label: "Somente leitura?", Help: "esconde as tools que lançam ou alteram horas",
				Kind: provider.Bool, Advanced: true, Validate: settings.CheckBool},
			{Env: "SEVENPACE_ENABLE_DELETE", Label: "Permitir excluir lançamentos?", Help: "expõe delete_worklog",
				Kind: provider.Bool, Advanced: true, Validate: settings.CheckBool},
		},
		Instructions: "- Datas são AAAA-MM-DD no fuso do usuário; \"hoje\"/\"ontem\" devem ser convertidos para datas.\n" +
			"- Antes de lançar horas, use time_summary para ver o que já existe e o que falta.",
		Enabled: sevenPaceEnabled,
		Build: func(bc *provider.BuildContext) (provider.Instance, error) {
			cfg, err := loadSevenPaceConfig(bc.Getenv)
			if err != nil {
				return nil, err
			}
			inst := &sevenPaceInstance{cfg: cfg, sp: NewSevenPace(cfg.APIRoot, cfg.Token, bc.HTTP)}
			if b, ok := bc.Get(boardsKey).(*boardsInstance); ok {
				inst.az = b.az // optional: work item titles in time_summary
			}
			return inst, nil
		},
	}
}

type sevenPaceInstance struct {
	cfg *sevenPaceConfig
	sp  *SevenPace
	az  *AzDO
}

func (i *sevenPaceInstance) Register(s *mcp.Server) { registerSevenPaceTools(s, i.sp, i.az, i.cfg) }

func (i *sevenPaceInstance) Check(ctx context.Context, w io.Writer) error {
	c := ui.For(w)
	line := func(format string, args ...any) { fmt.Fprintln(w, c.Mark(fmt.Sprintf(format, args...))) }
	fmt.Fprintln(w, c.BoldCyan("7pace:"), i.cfg.APIRoot)
	defer func() {
		fmt.Fprintln(w, c.Dim(fmt.Sprintf("  somente leitura: %v | exclusão habilitada: %v", i.cfg.ReadOnly, i.cfg.EnableDelete && !i.cfg.ReadOnly)))
	}()
	me, err := i.sp.Me(ctx)
	if err != nil {
		line("  ✗ %v", err)
		return err
	}
	line("  ✓ conectado como %s (%s)", c.Bold(me.User.Name), me.User.UniqueName)
	if at, err := i.sp.ActivityTypes(ctx); err == nil {
		line("  ✓ %d tipos de atividade", len(at))
	}
	return nil
}
