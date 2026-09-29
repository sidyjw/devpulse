package azuredevops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sidiney/pm-mcp/internal/httpx"
	"github.com/sidiney/pm-mcp/internal/mcp"
	"github.com/sidiney/pm-mcp/internal/provider"
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
