package azuredevops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sidyjw/devpulse/internal/httpx"
	"github.com/sidyjw/devpulse/internal/mcp"
	"github.com/sidyjw/devpulse/internal/provider"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestSevenPaceConfig(t *testing.T) {
	cfg, err := loadSevenPaceConfig(env(map[string]string{"SEVENPACE_ORGANIZATION": "minha-org", "SEVENPACE_TOKEN": "t"}))
	if err != nil || cfg.APIRoot != "https://minha-org.timehub.7pace.com/api" {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
	bad := []map[string]string{
		{"SEVENPACE_ORGANIZATION": "evil.com/x", "SEVENPACE_TOKEN": "t"},
		{"SEVENPACE_ORGANIZATION": "org"},
		{"SEVENPACE_BASE_URL": "http://org.timehub.7pace.com/api", "SEVENPACE_TOKEN": "t"},
		{"SEVENPACE_BASE_URL": "https://user:pw@host/api", "SEVENPACE_TOKEN": "t"},
		{"SEVENPACE_ORGANIZATION": "org", "SEVENPACE_TOKEN": "t", "SEVENPACE_TOKEN_FILE": "/x"},
		{"SEVENPACE_TOKEN": "t"},
	}
	for i, m := range bad {
		if _, err := loadSevenPaceConfig(env(m)); err == nil {
			t.Errorf("caso %d deveria falhar: %v", i, m)
		}
	}
}

func TestBoardsConfig(t *testing.T) {
	cfg, err := loadBoardsConfig(env(map[string]string{"AZURE_DEVOPS_ORG_URL": "https://dev.azure.com/org/", "AZURE_DEVOPS_PAT": "p", "AZURE_DEVOPS_PROJECT": "Proj"}))
	if err != nil || cfg.OrgURL != "https://dev.azure.com/org" || cfg.Project != "Proj" {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
	for i, m := range []map[string]string{
		{"AZURE_DEVOPS_ORG_URL": "https://dev.azure.com/org"},
		{"AZURE_DEVOPS_ORG_URL": "http://dev.azure.com/org", "AZURE_DEVOPS_PAT": "p"},
	} {
		if _, err := loadBoardsConfig(env(m)); err == nil {
			t.Errorf("caso %d deveria falhar: %v", i, m)
		}
	}
}

func TestTokenFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(p, []byte("  abc123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadSevenPaceConfig(env(map[string]string{"SEVENPACE_ORGANIZATION": "org", "SEVENPACE_TOKEN_FILE": p}))
	if err != nil || cfg.Token != "abc123" {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
}

func TestSecretDefaultReusesLegacyFiles(t *testing.T) {
	home := t.TempDir()
	def := secretDefault("7pace-token", "token")
	write := func(p string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	current := filepath.Join(home, ".devpulse", "7pace-token")
	if got := def(home); got != current {
		t.Errorf("sem arquivos: %s", got)
	}
	sevenPace := filepath.Join(home, ".7pace", "token")
	write(sevenPace)
	if got := def(home); got != sevenPace {
		t.Errorf("com ~/.7pace: %s", got)
	}
	pmMcp := filepath.Join(home, ".pm-mcp", "7pace-token")
	write(pmMcp)
	if got := def(home); got != pmMcp {
		t.Errorf("com ~/.pm-mcp: %s", got)
	}
	write(current)
	if got := def(home); got != current {
		t.Errorf("com ~/.devpulse: %s", got)
	}
}

// Activation by environment reproduces the old behavior: each component is
// optional, but at least one must be configured.
func TestActivation(t *testing.T) {
	ps := []provider.Provider{Provider()}
	hc := httpx.NewHTTPClient(time.Second)
	sp := map[string]string{"SEVENPACE_ORGANIZATION": "org", "SEVENPACE_TOKEN": "t"}
	az := map[string]string{"AZURE_DEVOPS_ORG_URL": "https://dev.azure.com/org", "AZURE_DEVOPS_PAT": "p"}
	both := map[string]string{}
	for k, v := range sp {
		both[k] = v
	}
	for k, v := range az {
		both[k] = v
	}

	tools := func(m map[string]string) ([]string, string) {
		act, instr, err := provider.Activate(ps, env(m), hc, "base")
		if err != nil {
			t.Fatalf("%v: %v", m, err)
		}
		s := mcp.NewServer("t", "0", instr)
		var keys []string
		for _, a := range act {
			a.Instance.Register(s)
			keys = append(keys, a.Key)
		}
		return keys, strings.Join(s.ToolNames(), ",") + "|" + instr
	}

	if k, out := tools(sp); strings.Join(k, ",") != "azuredevops.sevenpace" || !strings.Contains(out, "log_time") || strings.Contains(out, "list_sprints") {
		t.Errorf("só 7pace: %v %s", k, out)
	}
	if k, out := tools(az); strings.Join(k, ",") != "azuredevops.boards" || strings.Contains(out, "log_time") || !strings.Contains(out, "query_work_items") {
		t.Errorf("só Boards: %v %s", k, out)
	}
	k, out := tools(both)
	if strings.Join(k, ",") != "azuredevops.boards,azuredevops.sevenpace" {
		t.Errorf("os dois: %v", k)
	}
	if !strings.HasPrefix(strings.SplitN(out, "|", 2)[1], "base\n") || !strings.Contains(out, "time_summary para ver") {
		t.Errorf("instructions não foram compostas: %s", out)
	}

	if _, _, err := provider.Activate(ps, env(nil), hc, ""); err == nil || !strings.Contains(err.Error(), "nada configurado") {
		t.Errorf("sem configuração deveria falhar: %v", err)
	}
	if _, _, err := provider.Activate(ps, env(map[string]string{"SEVENPACE_ORGANIZATION": "org"}), hc, ""); err == nil {
		t.Errorf("7pace incompleto deveria falhar")
	}
}

func TestReposActivation(t *testing.T) {
	ps := []provider.Provider{Provider()}
	hc := httpx.NewHTTPClient(time.Second)
	m := map[string]string{"AZURE_DEVOPS_ORG_URL": "https://dev.azure.com/org", "AZURE_DEVOPS_PAT": "p", "AZURE_DEVOPS_REPOS": "true"}
	act, instr, err := provider.Activate(ps, env(m), hc, "")
	if err != nil {
		t.Fatal(err)
	}
	s := mcp.NewServer("t", "0", instr)
	var keys []string
	for _, a := range act {
		a.Instance.Register(s)
		keys = append(keys, a.Key)
	}
	names := strings.Join(s.ToolNames(), ",")
	if strings.Join(keys, ",") != "azuredevops.boards,azuredevops.repos" || !strings.Contains(names, "create_pull_request") || !strings.Contains(instr, "git remote get-url origin") {
		t.Errorf("keys=%v tools=%s", keys, names)
	}
	for _, r := range act {
		if ri, ok := r.Instance.(*reposInstance); ok && ri.az != act[0].Instance.(*boardsInstance).az {
			t.Error("o Repos deveria usar o cliente do Boards")
		}
	}

	m["AZURE_DEVOPS_REPOS_READ_ONLY"] = "true"
	act, _, _ = provider.Activate(ps, env(m), hc, "")
	s = mcp.NewServer("t", "0", "")
	for _, a := range act {
		a.Instance.Register(s)
	}
	if names := strings.Join(s.ToolNames(), ","); strings.Contains(names, "create_branch") || !strings.Contains(names, "create_work_item") {
		t.Errorf("Repos somente leitura: %s", names)
	}

	if _, _, err := provider.Activate(ps, env(map[string]string{"AZURE_DEVOPS_REPOS": "true"}), hc, ""); err == nil || !strings.Contains(err.Error(), "AZURE_DEVOPS_ORG_URL") {
		t.Errorf("Repos sem conexão deveria falhar: %v", err)
	}
	if act, _, _ := provider.Activate(ps, env(map[string]string{"AZURE_DEVOPS_ORG_URL": "https://dev.azure.com/org", "AZURE_DEVOPS_PAT": "p"}), hc, ""); len(act) != 1 {
		t.Errorf("sem AZURE_DEVOPS_REPOS o Repos não deveria ligar: %v", act)
	}
}

func TestSevenPaceUsesBoardsWhenAvailable(t *testing.T) {
	m := map[string]string{"SEVENPACE_ORGANIZATION": "org", "SEVENPACE_TOKEN": "t",
		"AZURE_DEVOPS_ORG_URL": "https://dev.azure.com/org", "AZURE_DEVOPS_PAT": "p"}
	act, _, err := provider.Activate([]provider.Provider{Provider()}, env(m), httpx.NewHTTPClient(time.Second), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range act {
		if sp, ok := a.Instance.(*sevenPaceInstance); ok && sp.az == nil {
			t.Error("7pace deveria receber o cliente do Boards")
		}
	}
}

func TestSettingsValidateLikeServer(t *testing.T) {
	for _, c := range Provider().Components {
		for _, s := range c.Settings {
			if s.Required && s.Kind != provider.SecretFile && s.Validate == nil {
				t.Errorf("%s: obrigatória sem validação", s.Env)
			}
		}
	}
	_, c := provider.Find([]provider.Provider{Provider()}, ProviderID, SevenPaceID)
	if c == nil || c.Settings[0].Validate("evil.com/x") == nil {
		t.Error("validação da organização deveria recusar evil.com/x")
	}
}
