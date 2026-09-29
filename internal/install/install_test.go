package install

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sidyjw/devpulse/internal/provider"
	"github.com/sidyjw/devpulse/internal/providers/azuredevops"
)

// ---------- helpers ----------

type recorder struct {
	mu    sync.Mutex
	calls []Cmd
	// reply returns the stdout/error for a command (nil = "", nil).
	reply func(c Cmd) (string, error)
}

func (r *recorder) run(_ context.Context, c Cmd) (string, error) {
	r.mu.Lock()
	r.calls = append(r.calls, c)
	r.mu.Unlock()
	if r.reply != nil {
		return r.reply(c)
	}
	return "", nil
}

func (r *recorder) named(name string) []Cmd {
	var out []Cmd
	for _, c := range r.calls {
		if c.Name == name || filepath.Base(c.Name) == name {
			out = append(out, c)
		}
	}
	return out
}

func fakeEnv(t *testing.T, goos string) (*Env, *recorder) {
	t.Helper()
	root := t.TempDir()
	rec := &recorder{}
	e := &Env{
		GOOS: goos, GOARCH: "amd64",
		Home:         filepath.Join(root, "home"),
		AppData:      filepath.Join(root, "appdata"),
		LocalAppData: filepath.Join(root, "localappdata"),
		XDGConfig:    filepath.Join(root, "home", ".config"),
		Cwd:          filepath.Join(root, "proj"),
		Exe:          filepath.Join(root, "build", "devpulse.exe"),
		Getenv: func(k string) string {
			if k == "USERNAME" {
				return "tester"
			}
			return ""
		},
		Environ:  func() []string { return []string{"PATH=/bin", "SEVENPACE_TOKEN=nao-pode-vazar"} },
		LookPath: func(string) (string, error) { return "", errors.New("not found") },
		Run:      rec.run,
	}
	for _, d := range []string{e.Home, e.Cwd, filepath.Dir(e.Exe)} {
		must(t, os.MkdirAll(d, 0o755))
	}
	must(t, os.WriteFile(e.Exe, []byte("binary"), 0o755))
	return e, rec
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Dir(path), 0o755))
	must(t, os.WriteFile(path, []byte(content), 0o600))
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	must(t, err)
	var m map[string]any
	must(t, json.Unmarshal(b, &m))
	return m
}

var testBuild = Build{Name: "devpulse", Version: "test", Providers: []provider.Provider{azuredevops.Provider()}}

func runCLI(t *testing.T, e *Env, stdin string, args ...string) (int, string) {
	t.Helper()
	var out bytes.Buffer
	code := run(context.Background(), args, testBuild, e, strings.NewReader(stdin), &out, &out)
	return code, out.String()
}

// ---------- JSON config editing ----------

func TestUpsertPreservesOtherKeysAndOrder(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cfg.json")
	writeFile(t, p, `{"preferences":{"theme":"dark"},"mcpServers":{"other":{"command":"x"}},"zeta":1}`)

	res, err := UpsertEntry(p, []string{"mcpServers"}, "devpulse", map[string]any{"command": "/bin/pm"}, false)
	must(t, err)
	if !res.Changed || res.Backup == "" || !exists(res.Backup) {
		t.Fatalf("res = %+v", res)
	}
	b, _ := os.ReadFile(p)
	s := string(b)
	if !(strings.Index(s, "preferences") < strings.Index(s, "mcpServers") && strings.Index(s, "mcpServers") < strings.Index(s, "zeta")) {
		t.Errorf("ordem das chaves mudou:\n%s", s)
	}
	m := readJSON(t, p)
	servers := m["mcpServers"].(map[string]any)
	if servers["other"] == nil || servers["devpulse"].(map[string]any)["command"] != "/bin/pm" {
		t.Errorf("servers = %v", servers)
	}
	if m["preferences"].(map[string]any)["theme"] != "dark" {
		t.Errorf("preferences perdido: %v", m)
	}

	// idempotent
	res, err = UpsertEntry(p, []string{"mcpServers"}, "devpulse", map[string]any{"command": "/bin/pm"}, false)
	must(t, err)
	if res.Changed {
		t.Error("segunda gravação idêntica não deveria alterar o arquivo")
	}

	res, err = RemoveEntry(p, []string{"mcpServers"}, "devpulse", false)
	must(t, err)
	if !res.Changed || readJSON(t, p)["mcpServers"].(map[string]any)["devpulse"] != nil {
		t.Error("remoção falhou")
	}
}

func TestUpsertCreatesFileAndNestedKeys(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "claude.json")
	_, err := UpsertEntry(p, []string{"projects", "C:/x/y", "mcpServers"}, "devpulse", map[string]any{"command": "c"}, false)
	must(t, err)
	m := readJSON(t, p)
	got := m["projects"].(map[string]any)["C:/x/y"].(map[string]any)["mcpServers"].(map[string]any)["devpulse"]
	if got == nil {
		t.Fatalf("entrada aninhada ausente: %v", m)
	}
	entries, err := ReadEntries(p, []string{"projects", "C:/x/y", "mcpServers"})
	if err != nil || entries["devpulse"].Command != "c" {
		t.Errorf("ReadEntries = %v, %v", entries, err)
	}
}

func TestRefusesNonJSONAndDryRun(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	orig := "{\n  // comentário\n  \"a\": 1\n}"
	writeFile(t, p, orig)
	if _, err := UpsertEntry(p, []string{"mcpServers"}, "x", map[string]any{}, false); !errors.Is(err, ErrNotJSON) {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(p); string(b) != orig {
		t.Error("arquivo com comentários foi alterado")
	}

	q := filepath.Join(dir, "new.json")
	res, err := UpsertEntry(q, []string{"mcpServers"}, "x", map[string]any{"command": "c"}, true)
	must(t, err)
	if !res.Changed || exists(q) {
		t.Error("dry-run não deveria criar o arquivo")
	}
}

// ---------- targets per OS ----------

func TestDesktopPaths(t *testing.T) {
	e, _ := fakeEnv(t, "windows")
	if got := (claudeDesktop{}).Target(e, "").Path; got != filepath.Join(e.AppData, "Claude", "claude_desktop_config.json") {
		t.Errorf("windows = %s", got)
	}
	msix := filepath.Join(e.LocalAppData, "Packages", "Claude_pzs8sxrjxfjjc", "LocalCache", "Roaming", "Claude")
	must(t, os.MkdirAll(msix, 0o755))
	tg := (claudeDesktop{}).Target(e, "")
	if tg.Path != filepath.Join(msix, "claude_desktop_config.json") || !strings.Contains(tg.Note, "MSIX") {
		t.Errorf("MSIX = %+v", tg)
	}
	if !(claudeDesktop{}).Detect(e) || !(claudeCowork{}).Detect(e) {
		t.Error("Desktop/Cowork deveriam ser detectados")
	}
	if c := (claudeCowork{}).Target(e, ""); c.Key() != tg.Key() {
		t.Error("Cowork deveria usar o arquivo do Desktop")
	}

	e.GOOS = "darwin"
	if got := (claudeDesktop{}).Target(e, "").Path; got != filepath.Join(e.Home, "Library", "Application Support", "Claude", "claude_desktop_config.json") {
		t.Errorf("darwin = %s", got)
	}
	e.GOOS = "linux"
	if got := (claudeDesktop{}).Target(e, "").Path; got != filepath.Join(e.XDGConfig, "Claude", "claude_desktop_config.json") {
		t.Errorf("linux = %s", got)
	}
	if (claudeCowork{}).Detect(e) {
		t.Error("Cowork não existe no Linux")
	}
}

func TestClaudeCodeScopes(t *testing.T) {
	e, _ := fakeEnv(t, "linux")
	c := claudeCode{}
	if tg := c.Target(e, "user"); tg.Path != filepath.Join(e.Home, ".claude.json") || strings.Join(tg.Keys, "/") != "mcpServers" {
		t.Errorf("user = %+v", tg)
	}
	if tg := c.Target(e, "local"); tg.Keys[0] != "projects" || tg.Keys[1] != filepath.ToSlash(e.Cwd) {
		t.Errorf("local = %+v", tg)
	}
	if tg := c.Target(e, "project"); tg.Path != filepath.Join(e.Cwd, ".mcp.json") {
		t.Errorf("project = %+v", tg)
	}
}

func TestClaudeCodeUsesCLIWhenSafe(t *testing.T) {
	e, rec := fakeEnv(t, "linux")
	e.LookPath = func(string) (string, error) { return "/usr/local/bin/claude", nil }
	c := claudeCode{}
	tg := c.Target(e, "user")
	writeFile(t, tg.Path, `{"mcpServers":{"devpulse":{"command":"old"}}}`)
	spec := Spec{Name: "devpulse", Command: "/opt/devpulse", Env: map[string]string{"A": "1"}}
	var out bytes.Buffer
	must(t, c.Install(context.Background(), e, tg, spec, Opts{Out: &out}))
	calls := rec.named("claude")
	if len(calls) != 2 || calls[0].Args[1] != "remove" || calls[1].Args[1] != "add-json" {
		t.Fatalf("chamadas = %+v", calls)
	}
	var entry map[string]any
	must(t, json.Unmarshal([]byte(calls[1].Args[3]), &entry))
	if entry["type"] != "stdio" || entry["command"] != "/opt/devpulse" || calls[1].Args[5] != "user" {
		t.Errorf("add-json = %v %v", entry, calls[1].Args)
	}
	if readJSON(t, tg.Path)["mcpServers"].(map[string]any)["devpulse"].(map[string]any)["command"] != "old" {
		t.Error("com o CLI disponível o arquivo não deveria ser editado diretamente")
	}

	// npm shim on Windows: never pass JSON through cmd.exe; edit the file
	e2, rec2 := fakeEnv(t, "windows")
	e2.LookPath = func(string) (string, error) { return `C:\npm\claude.cmd`, nil }
	tg2 := c.Target(e2, "user")
	must(t, c.Install(context.Background(), e2, tg2, spec, Opts{Out: &out}))
	if len(rec2.named("claude.cmd")) != 0 {
		t.Error("não deveria executar claude.cmd")
	}
	if readJSON(t, tg2.Path)["mcpServers"].(map[string]any)["devpulse"].(map[string]any)["type"] != "stdio" {
		t.Error("entrada do Claude Code deveria ter type=stdio")
	}
}

// ---------- full flows ----------

func TestInstallNonInteractiveMigratesLegacy(t *testing.T) {
	e, rec := fakeEnv(t, "windows")
	cfg := filepath.Join(e.AppData, "Claude", "claude_desktop_config.json")
	tok := filepath.Join(e.Home, ".7pace", "token")
	writeFile(t, tok, "segredo")
	writeFile(t, cfg, `{"preferences":{"x":true},"mcpServers":{"7pace":{"command":"C:\\old\\7pace-mcp.exe","env":{
		"SEVENPACE_ORGANIZATION":"velha","SEVENPACE_TOKEN_FILE":`+jsonStr(tok)+`,"SEVENPACE_HTTP_TIMEOUT":"45s","MINHA_VAR":"x"}}}}`)
	rec.reply = func(c Cmd) (string, error) { return "7pace: ok\n", nil }

	code, out := runCLI(t, e, "", "install", "--yes", "--harness", "claude", "--app", "desktop,cowork",
		"--bin-dir", filepath.Join(e.LocalAppData, "Programs", "devpulse"))
	if code != 0 {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if !strings.Contains(out, "gravado uma vez só") {
		t.Errorf("Desktop e Cowork deveriam ser deduplicados:\n%s", out)
	}
	m := readJSON(t, cfg)
	servers := m["mcpServers"].(map[string]any)
	if servers["7pace"] != nil {
		t.Errorf("entrada antiga deveria ter sido removida: %v", servers)
	}
	entry := servers["devpulse"].(map[string]any)
	env := entry["env"].(map[string]any)
	if env["SEVENPACE_ORGANIZATION"] != "velha" || env["SEVENPACE_TOKEN_FILE"] != tok || env["MINHA_VAR"] != "x" || env["DEVPULSE_HTTP_TIMEOUT"] != "45s" {
		t.Errorf("env = %v", env)
	}
	if env["SEVENPACE_HTTP_TIMEOUT"] != nil || env["AZURE_DEVOPS_ORG_URL"] != nil {
		t.Errorf("variáveis inesperadas: %v", env)
	}
	wantBin := filepath.Join(e.LocalAppData, "Programs", "devpulse", "devpulse.exe")
	if entry["command"] != wantBin || !exists(wantBin) {
		t.Errorf("command = %v", entry["command"])
	}
	if m["preferences"] == nil {
		t.Error("preferences perdido")
	}
	// -check ran with the new env and without inherited secrets
	checks := rec.named("devpulse.exe")
	if len(checks) != 1 || checks[0].Args[0] != "-check" {
		t.Fatalf("check = %+v", rec.calls)
	}
	joined := strings.Join(checks[0].Env, "\n")
	if strings.Contains(joined, "nao-pode-vazar") || !strings.Contains(joined, "SEVENPACE_ORGANIZATION=velha") || !strings.Contains(joined, "PATH=/bin") {
		t.Errorf("env do -check = %v", checks[0].Env)
	}
}

func TestInstallMigratesPmMcp(t *testing.T) {
	e, rec := fakeEnv(t, "windows")
	cfg := filepath.Join(e.AppData, "Claude", "claude_desktop_config.json")
	tok := filepath.Join(e.Home, ".pm-mcp", "7pace-token")
	oldBin := filepath.Join(e.LocalAppData, "Programs", "pm-mcp", "pm-mcp.exe")
	writeFile(t, tok, "segredo")
	writeFile(t, oldBin, "binary")
	writeFile(t, cfg, `{"mcpServers":{"pm-mcp":{"command":`+jsonStr(oldBin)+`,"env":{
		"SEVENPACE_ORGANIZATION":"org","SEVENPACE_TOKEN_FILE":`+jsonStr(tok)+`,"PM_MCP_HTTP_TIMEOUT":"45s"}}}}`)
	rec.reply = func(c Cmd) (string, error) { return "7pace: ok\n", nil }

	code, out := runCLI(t, e, "", "install", "--yes", "--harness", "claude", "--app", "desktop")
	if code != 0 {
		t.Fatalf("code=%d\n%s", code, out)
	}
	servers := readJSON(t, cfg)["mcpServers"].(map[string]any)
	if servers["pm-mcp"] != nil {
		t.Errorf("entrada pm-mcp deveria ter sido removida: %v", servers)
	}
	entry := servers["devpulse"].(map[string]any)
	env := entry["env"].(map[string]any)
	if env["SEVENPACE_ORGANIZATION"] != "org" || env["SEVENPACE_TOKEN_FILE"] != tok || env["DEVPULSE_HTTP_TIMEOUT"] != "45s" || env["PM_MCP_HTTP_TIMEOUT"] != nil {
		t.Errorf("env = %v", env)
	}
	if want := filepath.Join(e.LocalAppData, "Programs", "devpulse", "devpulse.exe"); entry["command"] != want {
		t.Errorf("command = %v", entry["command"])
	}
	if !strings.Contains(out, "versão antiga") || !strings.Contains(out, oldBin) {
		t.Errorf("deveria indicar a migração e o executável antigo:\n%s", out)
	}
}

func TestUninstallFindsLegacyEntries(t *testing.T) {
	e, _ := fakeEnv(t, "windows")
	cfg := filepath.Join(e.AppData, "Claude", "claude_desktop_config.json")
	writeFile(t, cfg, `{"mcpServers":{"pm-mcp":{"command":"a"},"7pace":{"command":"b"},"outro":{"command":"c"}}}`)

	code, out := runCLI(t, e, "", "uninstall", "--yes", "--harness", "claude", "--app", "desktop")
	if code != 0 {
		t.Fatalf("code=%d\n%s", code, out)
	}
	servers := readJSON(t, cfg)["mcpServers"].(map[string]any)
	if servers["pm-mcp"] != nil || servers["7pace"] != nil || servers["outro"] == nil {
		t.Errorf("servers = %v", servers)
	}
}

func jsonStr(s string) string { b, _ := json.Marshal(s); return string(b) }

func TestInstallInteractive(t *testing.T) {
	e, rec := fakeEnv(t, "windows")
	e.LookPath = func(n string) (string, error) {
		if n == "powershell.exe" {
			return "C:\\ps\\powershell.exe", nil
		}
		return "", errors.New("not found")
	}
	must(t, os.MkdirAll(filepath.Join(e.AppData, "Claude"), 0o755)) // Desktop "installed"
	rec.reply = func(c Cmd) (string, error) {
		if strings.Contains(strings.Join(c.Args, " "), "Get-Clipboard") {
			return "tok-do-clipboard\r\n", nil
		}
		return "", nil
	}
	cfg := filepath.Join(e.AppData, "Claude", "claude_desktop_config.json")
	custom := filepath.Join(e.Home, "custom", "claude.json")
	tokFile := filepath.Join(e.Home, "tokens", "7p")
	answers := strings.Join([]string{
		"",       // harness: Claude (detected)
		"2",      // apps: only Claude Desktop
		custom,   // user changes the config path
		"9", "2", // components: invalid, then 7pace
		"org inválida", "minhaorg", // validation retries
		tokFile, // token file
		"",      // create from clipboard? yes
		"",      // Enter after copying
		"s",     // advanced options
		"",      // SEVENPACE_BASE_URL (empty)
		"s",     // read-only
		"n",     // delete
		"",      // timeout default
		"",      // apply
	}, "\n") + "\n"
	code, out := runCLI(t, e, answers, "install", "--no-copy", "--skip-check")
	if code != 0 {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if exists(cfg) {
		t.Error("o caminho padrão não deveria ser usado quando o usuário troca o arquivo")
	}
	env := readJSON(t, custom)["mcpServers"].(map[string]any)["devpulse"].(map[string]any)["env"].(map[string]any)
	if env["SEVENPACE_ORGANIZATION"] != "minhaorg" || env["SEVENPACE_TOKEN_FILE"] != tokFile || env["SEVENPACE_READ_ONLY"] != "true" {
		t.Errorf("env = %v", env)
	}
	if _, ok := env["SEVENPACE_ENABLE_DELETE"]; ok {
		t.Error("bool falso não deveria ser gravado")
	}
	if _, ok := env["DEVPULSE_HTTP_TIMEOUT"]; ok {
		t.Error("timeout padrão não deveria ser gravado")
	}
	b, err := os.ReadFile(tokFile)
	if err != nil || string(b) != "tok-do-clipboard" {
		t.Errorf("token = %q, %v", b, err)
	}
	if strings.Contains(out, "tok-do-clipboard") {
		t.Error("o token apareceu na saída")
	}
	if len(rec.named("icacls")) != 1 {
		t.Error("icacls deveria restringir o arquivo no Windows")
	}
	cleared := false
	for _, c := range rec.named("powershell.exe") {
		if strings.Contains(strings.Join(c.Args, " "), "Set-Clipboard") {
			cleared = true
		}
	}
	if !cleared {
		t.Error("a área de transferência deveria ser limpa")
	}
}

func TestInstallDryRunWritesNothing(t *testing.T) {
	e, rec := fakeEnv(t, "linux")
	code, out := runCLI(t, e, "", "install", "--yes", "--dry-run", "--harness", "claude", "--app", "desktop",
		"--components", "boards", "--set", "AZURE_DEVOPS_ORG_URL=https://dev.azure.com/org",
		"--set", "AZURE_DEVOPS_PAT_FILE=~/pat")
	if code != 0 {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if exists(filepath.Join(e.XDGConfig, "Claude", "claude_desktop_config.json")) || exists(filepath.Join(e.Home, ".local", "bin", "devpulse")) {
		t.Error("dry-run gravou algo")
	}
	if len(rec.calls) != 0 {
		t.Errorf("dry-run executou comandos: %+v", rec.calls)
	}
	if !strings.Contains(out, filepath.Join(e.Home, "pat")) || !strings.Contains(out, "nada foi gravado") {
		t.Errorf("saída:\n%s", out)
	}
}

func TestInstallValidationErrors(t *testing.T) {
	e, _ := fakeEnv(t, "linux")
	cases := [][]string{
		{"--set", "NAO_EXISTE=1"},
		{"--set", "SEVENPACE_ORGANIZATION=evil.com/x"},
		{"--harness", "nope"},
		{"--app", "nope"},
		{"--components", "nope"},
	}
	for _, extra := range cases {
		args := append([]string{"install", "--yes", "--harness", "claude", "--app", "desktop", "--no-copy", "--skip-check",
			"--set", "SEVENPACE_TOKEN_FILE=/x"}, extra...)
		if code, out := runCLI(t, e, "", args...); code == 0 {
			t.Errorf("%v deveria falhar:\n%s", extra, out)
		}
	}
	// failing -check aborts under --yes
	e.Run = func(context.Context, Cmd) (string, error) { return "7pace: ✗ 401", errors.New("exit status 1") }
	code, out := runCLI(t, e, "", "install", "--yes", "--harness", "claude", "--app", "desktop", "--no-copy",
		"--components", "sevenpace", "--set", "SEVENPACE_ORGANIZATION=org", "--set", "SEVENPACE_TOKEN_FILE=/x")
	if code == 0 || exists(filepath.Join(e.XDGConfig, "Claude", "claude_desktop_config.json")) {
		t.Errorf("check falho deveria abortar sem gravar:\n%s", out)
	}
}

func TestGenericPrintsSnippets(t *testing.T) {
	e, _ := fakeEnv(t, "darwin")
	code, out := runCLI(t, e, "", "install", "--yes", "--harness", "generic", "--no-copy", "--skip-check",
		"--components", "boards", "--set", "AZURE_DEVOPS_ORG_URL=https://dev.azure.com/org", "--set", "AZURE_DEVOPS_PAT_FILE="+filepath.Join(e.Home, "p"))
	if code != 0 {
		t.Fatalf("code=%d\n%s", code, out)
	}
	for _, want := range []string{`"mcpServers"`, `"servers"`, "[mcp_servers.devpulse]", `AZURE_DEVOPS_PAT_FILE = ` + tomlString(filepath.Join(e.Home, "p"))} {
		if !strings.Contains(out, want) {
			t.Errorf("saída sem %q:\n%s", want, out)
		}
	}
}

func TestGenericWritesCustomFileAndUninstall(t *testing.T) {
	e, _ := fakeEnv(t, "linux")
	p := filepath.Join(e.Home, ".cursor", "mcp.json")
	args := []string{"--yes", "--harness", "generic", "--config", p, "--key", "mcpServers"}
	code, out := runCLI(t, e, "", append(append([]string{"install"}, args...), "--no-copy", "--skip-check",
		"--components", "boards", "--set", "AZURE_DEVOPS_ORG_URL=https://dev.azure.com/org", "--set", "AZURE_DEVOPS_PAT_FILE="+filepath.Join(e.Home, "p"))...)
	if code != 0 {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if readJSON(t, p)["mcpServers"].(map[string]any)["devpulse"] == nil {
		t.Fatal("entrada não gravada")
	}
	code, out = runCLI(t, e, "", append([]string{"uninstall"}, args...)...)
	if code != 0 || readJSON(t, p)["mcpServers"].(map[string]any)["devpulse"] != nil {
		t.Fatalf("uninstall falhou (%d):\n%s", code, out)
	}
}

func TestDetect(t *testing.T) {
	e, _ := fakeEnv(t, "darwin")
	cfg := filepath.Join(e.Home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	writeFile(t, cfg, `{"mcpServers":{"7pace":{"command":"/old"}}}`)
	code, out := runCLI(t, e, "", "detect")
	if code != 0 || !strings.Contains(out, "macOS (amd64)") || !strings.Contains(out, `"7pace" → /old (antiga)`) || !strings.Contains(out, "Azure DevOps (azuredevops)") {
		t.Errorf("detect:\n%s", out)
	}
}

// ---------- prompts and secrets ----------

func TestPrompter(t *testing.T) {
	var out bytes.Buffer
	p := NewPrompter(strings.NewReader("\nabc\n5\n1, 3\n"), &out, false)
	if v, _ := p.Input("x", "def", nil); v != "def" {
		t.Errorf("Enter deveria manter o padrão: %q", v)
	}
	if v, _ := p.Input("x", "def", nil); v != "abc" {
		t.Errorf("v = %q", v)
	}
	idx, err := p.MultiSelect("t", []Option{{Label: "a"}, {Label: "b"}, {Label: "c"}}, []bool{true, false, false})
	if err != nil || len(idx) != 2 || idx[0] != 0 || idx[1] != 2 {
		t.Errorf("idx = %v err = %v", idx, err)
	}
	if _, err := p.Input("x", "", nil); !errors.Is(err, errNoInput) {
		t.Errorf("fim da entrada deveria dar errNoInput: %v", err)
	}
	y := NewPrompter(strings.NewReader(""), &out, true)
	if _, err := y.Input("obrigatório", "", required); err == nil {
		t.Error("--yes sem valor obrigatório deveria falhar")
	}
}

func TestCheckToken(t *testing.T) {
	for _, bad := range []string{"", "a\nb", "com espaço", strings.Repeat("x", 5000)} {
		if checkToken(bad) == nil {
			t.Errorf("%q deveria ser recusado", bad)
		}
	}
	if checkToken("abc123-_.") != nil {
		t.Error("token válido recusado")
	}
}

func TestWriteSecretFilePermissions(t *testing.T) {
	e, _ := fakeEnv(t, "linux")
	p := filepath.Join(e.Home, ".devpulse", "tok")
	must(t, WriteSecretFile(context.Background(), e, p, "s3cr3t"))
	fi, err := os.Stat(p)
	must(t, err)
	if b, _ := os.ReadFile(p); string(b) != "s3cr3t" {
		t.Errorf("conteúdo = %q", b)
	}
	if os.PathSeparator == '/' && fi.Mode().Perm() != 0o600 {
		t.Errorf("perm = %o", fi.Mode().Perm())
	}
}
