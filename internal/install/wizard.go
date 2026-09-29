package install

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/sidiney/pm-mcp/internal/provider"
	"github.com/sidiney/pm-mcp/internal/settings"
	"github.com/sidiney/pm-mcp/internal/ui"
)

type wizard struct {
	ctx context.Context
	b   Build
	e   *Env
	o   *options
	p   *Prompter
	out io.Writer
	c   ui.Palette
}

func (w *wizard) printf(format string, args ...any) { fmt.Fprintf(w.out, format, args...) }

// status prints one line that starts with a status marker (✓ ✗ ! →).
func (w *wizard) status(format string, args ...any) {
	fmt.Fprintln(w.out, w.c.Mark(fmt.Sprintf(format, args...)))
}

// note prints a secondary, discreet line.
func (w *wizard) note(format string, args ...any) {
	fmt.Fprintln(w.out, w.c.Dim(fmt.Sprintf(format, args...)))
}

func (w *wizard) section(title string) { w.printf("\n%s\n", w.c.Section(title)) }

// header is the banner printed at the start of install and uninstall.
func (w *wizard) header(action string) {
	w.printf("%s\n%s\n", w.c.Title(w.b.Name, w.b.Version, action), w.c.Field("Sistema", w.e.Describe()))
}

// ---------- harness, apps and targets ----------

type selection struct {
	apps    []App
	targets []Target // deduplicated by file location
}

func (w *wizard) chooseTargets() (*selection, error) {
	w.section("Onde instalar")
	var h Harness
	if w.o.harness != "" {
		if h = findHarness(w.o.harness); h == nil {
			return nil, fmt.Errorf("harness desconhecida %q (opções: %s)", w.o.harness, harnessIDs())
		}
	} else {
		hs := Harnesses()
		opts := make([]Option, len(hs))
		def := -1
		for i, hh := range hs {
			var found []string
			for _, a := range hh.Apps() {
				if a.Detect(w.e) {
					found = append(found, a.Name())
				}
			}
			opts[i] = Option{Label: hh.Name()}
			if len(found) > 0 {
				opts[i].Hint = "detectado: " + strings.Join(found, ", ")
				if def < 0 {
					def = i
				}
			} else if hh.ID() == "generic" {
				opts[i].Hint = "mostra a configuração para qualquer cliente MCP"
			}
		}
		if def < 0 {
			def = 0
		}
		i, err := w.p.Select("Qual harness (cliente de IA)?", opts, def)
		if err != nil {
			return nil, err
		}
		h = hs[i]
	}

	apps := h.Apps()
	var chosen []App
	switch {
	case len(w.o.apps) > 0:
		for _, id := range w.o.apps {
			var a App
			var ids []string
			for _, aa := range apps {
				ids = append(ids, aa.ID())
				if aa.ID() == id {
					a = aa
				}
			}
			if a == nil {
				return nil, fmt.Errorf("app desconhecido %q para %s (opções: %s)", id, h.Name(), strings.Join(ids, ", "))
			}
			chosen = append(chosen, a)
		}
	case len(apps) == 1:
		chosen = apps
	default:
		opts := make([]Option, len(apps))
		defs := make([]bool, len(apps))
		preset := false
		for i, a := range apps {
			opts[i] = Option{Label: a.Name(), Hint: "não encontrado"}
			if a.Detect(w.e) {
				opts[i].Hint, defs[i], preset = "detectado", true, true
			}
		}
		if !preset {
			defs[0] = true
		}
		idx, err := w.p.MultiSelect("Em quais apps do "+h.Name()+"?", opts, defs)
		if err != nil {
			return nil, err
		}
		for _, i := range idx {
			chosen = append(chosen, apps[i])
		}
	}

	sel := &selection{apps: chosen}
	seen := map[string]string{}
	defaults := map[string]string{} // default location -> app that claimed it
	for _, a := range chosen {
		t, prev, err := w.targetFor(a, defaults)
		if err != nil {
			return nil, err
		}
		if prev != "" {
			w.note("  %s usa o mesmo arquivo que %s; ele será gravado uma vez só.", t.Label(), prev)
			continue
		}
		if t.Path != "" {
			if prev, ok := seen[t.Key()]; ok {
				w.note("  %s usa o mesmo arquivo que %s; ele será gravado uma vez só.", t.Label(), prev)
				continue
			}
			seen[t.Key()] = t.Label()
		}
		sel.targets = append(sel.targets, t)
	}
	return sel, nil
}

var scopeHints = map[string]string{
	"user":    "todos os seus projetos (recomendado)",
	"local":   "só no diretório atual, só para você",
	"project": "arquivo .mcp.json no diretório atual, compartilhado pelo repositório",
}

// targetFor resolves where app a is configured. When another chosen app
// already claimed the same default location (Cowork shares Desktop's file),
// it returns that app's label instead of asking again.
func (w *wizard) targetFor(a App, defaults map[string]string) (Target, string, error) {
	scope := ""
	if sc := a.Scopes(); sc != nil {
		if w.o.scope != "" {
			for _, s := range sc {
				if s == w.o.scope {
					scope = s
				}
			}
			if scope == "" {
				return Target{}, "", fmt.Errorf("escopo %q inválido para %s (opções: %s)", w.o.scope, a.Name(), strings.Join(sc, ", "))
			}
		} else {
			opts := make([]Option, len(sc))
			for i, s := range sc {
				opts[i] = Option{Label: s, Hint: scopeHints[s]}
			}
			i, err := w.p.Select("Escopo do "+a.Name()+"?", opts, 0)
			if err != nil {
				return Target{}, "", err
			}
			scope = sc[i]
		}
	}
	t := a.Target(w.e, scope)
	if t.Path != "" {
		if prev, ok := defaults[t.Key()]; ok {
			return t, prev, nil
		}
		defaults[t.Key()] = t.Label()
	}
	if w.o.key != "" {
		t.Keys, t.Custom = strings.Split(w.o.key, "."), true
	}
	if w.o.config != "" {
		t.Path, t.Custom = w.e.Expand(w.o.config), true
		return t, "", nil
	}

	if t.Path == "" { // generic: print, or write a JSON file the user names
		p, err := w.p.Input("Arquivo JSON do seu cliente (Enter = só mostrar a configuração)", "", nil)
		if err != nil || p == "" {
			return t, "", err
		}
		t.Path, t.Custom = w.e.Expand(p), true
		if w.o.key == "" {
			k, err := w.p.Input("Chave dos servidores nesse JSON (ex.: mcpServers, servers)", "mcpServers", required)
			if err != nil {
				return t, "", err
			}
			t.Keys = strings.Split(k, ".")
		}
		return t, "", nil
	}

	if t.Note != "" {
		w.printf("  %s %s\n", w.c.Bold(t.Label()+":"), w.c.Dim(t.Note))
	}
	if _, ok := a.(claudeCode); ok && claudeCLI(w.e) != "" {
		w.note("  (o Claude Code será configurado com `claude mcp add-json`; troque o arquivo só se você usa outro)")
	}
	p, err := w.p.Input("Arquivo de configuração do "+t.Label(), t.Path, required)
	if err != nil {
		return t, "", err
	}
	if p = w.e.Expand(p); !samePath(w.e, p, t.Path) {
		t.Path, t.Custom = p, true
	}
	return t, "", nil
}

// ---------- existing entries ----------

type found struct {
	t     Target
	name  string
	entry Entry
}

func (w *wizard) findExisting(ts []Target, names ...string) []found {
	var out []found
	for _, t := range ts {
		if t.Path == "" {
			continue
		}
		entries, err := ReadEntries(t.Path, t.Keys)
		if err != nil {
			w.status("  ! não consegui ler %s: %v", t.Path, err)
			continue
		}
		for _, n := range names {
			if e, ok := entries[n]; ok {
				out = append(out, found{t: t, name: n, entry: e})
			}
		}
	}
	return out
}

func (w *wizard) knownNames() []string {
	if w.o.name == LegacyName {
		return []string{w.o.name}
	}
	return []string{w.o.name, LegacyName}
}

// ---------- tool, components and settings ----------

// ownedEnv lists every variable the installer manages; any other variable
// already present in an existing entry is kept as is.
func ownedEnv(ps []provider.Provider) map[string]bool {
	m := map[string]bool{"SEVENPACE_HTTP_TIMEOUT": true}
	add := func(ss []provider.Setting) {
		for _, s := range ss {
			m[s.Env] = true
			if s.SecretAlt != "" {
				m[s.SecretAlt] = true
			}
		}
	}
	add(provider.GlobalSettings)
	for _, p := range ps {
		for _, c := range p.Components {
			add(c.Settings)
		}
	}
	return m
}

func (w *wizard) chooseComponents(prefill map[string]string) (*provider.Provider, []provider.Component, error) {
	w.section("Ferramenta de gestão")
	ps := w.b.Providers
	if len(ps) == 0 {
		return nil, nil, errors.New("nenhuma ferramenta de gestão disponível nesta versão")
	}
	pi := -1
	if w.o.providerID != "" {
		var ids []string
		for i, p := range ps {
			ids = append(ids, p.ID)
			if p.ID == w.o.providerID {
				pi = i
			}
		}
		if pi < 0 {
			return nil, nil, fmt.Errorf("ferramenta desconhecida %q (opções: %s)", w.o.providerID, strings.Join(ids, ", "))
		}
	} else if len(ps) == 1 {
		pi = 0
		w.printf("%s %s\n", w.c.Field("Ferramenta", w.c.Cyan(ps[0].Name)), w.c.Dim("(a única disponível nesta versão)"))
	} else {
		opts := make([]Option, len(ps))
		for i, p := range ps {
			opts[i] = Option{Label: p.Name}
		}
		var err error
		if pi, err = w.p.Select("Qual ferramenta de gestão você usa?", opts, 0); err != nil {
			return nil, nil, err
		}
	}
	p := &ps[pi]

	var comps []provider.Component
	if len(w.o.components) > 0 {
		for _, id := range w.o.components {
			_, c := provider.Find(ps, p.ID, id)
			if c == nil {
				var ids []string
				for _, cc := range p.Components {
					ids = append(ids, cc.ID)
				}
				return nil, nil, fmt.Errorf("componente desconhecido %q para %s (opções: %s)", id, p.Name, strings.Join(ids, ", "))
			}
			comps = append(comps, *c)
		}
		return p, comps, nil
	}
	getenv := func(k string) string { return prefill[k] }
	opts := make([]Option, len(p.Components))
	defs := make([]bool, len(p.Components))
	preset := false
	for i, c := range p.Components {
		opts[i] = Option{Label: c.Name, Hint: c.Description}
		if len(prefill) > 0 && c.Enabled(getenv) {
			defs[i], preset = true, true
		}
	}
	if !preset {
		for i := range defs {
			defs[i] = true
		}
	}
	idx, err := w.p.MultiSelect("Quais componentes do "+p.Name+" você quer integrar?", opts, defs)
	if err != nil {
		return nil, nil, err
	}
	for _, i := range idx {
		comps = append(comps, p.Components[i])
	}
	return p, comps, nil
}

func label(s provider.Setting) string {
	if s.Help != "" {
		return s.Label + " (" + s.Help + ")"
	}
	return s.Label
}

func (w *wizard) collectSettings(comps []provider.Component, prefill map[string]string) (map[string]string, error) {
	env := map[string]string{}
	owned := ownedEnv(w.b.Providers)
	for k, v := range prefill {
		if !owned[k] {
			env[k] = v // unknown variable from an existing entry: keep it
		}
	}
	for k := range w.o.set {
		if !owned[k] {
			return nil, fmt.Errorf("--set %s: variável desconhecida", k)
		}
	}
	// the legacy timeout name becomes the new one
	if v := prefill["SEVENPACE_HTTP_TIMEOUT"]; v != "" && prefill["PM_MCP_HTTP_TIMEOUT"] == "" {
		prefill["PM_MCP_HTTP_TIMEOUT"] = v
	}

	var advanced []provider.Setting
	for _, c := range comps {
		for _, s := range c.Settings {
			if s.Advanced {
				advanced = append(advanced, s)
			}
		}
	}
	advanced = append(advanced, provider.GlobalSettings...)

	for _, c := range comps {
		w.section(c.Name)
		for _, s := range c.Settings {
			if !s.Advanced {
				if err := w.askSetting(s, prefill, env); err != nil {
					return nil, err
				}
			}
		}
	}

	wantAdv := w.o.advanced
	for _, s := range advanced {
		if _, ok := w.o.set[s.Env]; ok || prefill[s.Env] != "" {
			wantAdv = true // keep customizations visible instead of dropping them
		}
	}
	if !wantAdv && !w.p.Yes {
		var names []string
		for _, s := range advanced {
			names = append(names, strings.TrimSuffix(s.Label, "?"))
		}
		var err error
		wantAdv, err = w.p.Confirm("\nAjustar opções avançadas ("+strings.ToLower(strings.Join(names, ", "))+")?", false)
		if err != nil {
			return nil, err
		}
	}
	if wantAdv {
		w.section("Opções avançadas")
		for _, s := range advanced {
			if err := w.askSetting(s, prefill, env); err != nil {
				return nil, err
			}
		}
	}
	// drop the default timeout: it is the server default anyway
	if env["PM_MCP_HTTP_TIMEOUT"] == "30s" {
		delete(env, "PM_MCP_HTTP_TIMEOUT")
	}
	return env, nil
}

func (w *wizard) defaultFor(s provider.Setting, prefill map[string]string) string {
	if v := prefill[s.Env]; v != "" {
		return v
	}
	if s.Default != nil {
		return s.Default(w.e.Home)
	}
	return ""
}

func (w *wizard) askSetting(s provider.Setting, prefill, env map[string]string) error {
	if s.Kind == provider.SecretFile {
		return w.askSecret(s, prefill, env)
	}
	validate := func(v string) error {
		if strings.TrimSpace(v) == "" {
			if s.Required {
				return errors.New("obrigatório")
			}
			return nil
		}
		if s.Validate != nil {
			return s.Validate(strings.TrimSpace(v))
		}
		if s.Kind == provider.Duration {
			_, err := settings.ParseTimeout(v)
			return err
		}
		return nil
	}
	if v, ok := w.o.set[s.Env]; ok {
		if err := validate(v); err != nil {
			return fmt.Errorf("--set %s: %w", s.Env, err)
		}
		w.p.answered(s.Label, v)
		if v != "" && !(s.Kind == provider.Bool && !settings.EnvBool(v)) {
			env[s.Env] = v
		}
		return nil
	}
	w.guide(s.Guide, prefill, env)
	def := w.defaultFor(s, prefill)
	if s.Kind == provider.Bool {
		yes, err := w.p.Confirm(label(s), settings.EnvBool(def))
		if err != nil {
			return err
		}
		if yes {
			env[s.Env] = "true"
		}
		return nil
	}
	v, err := w.p.Input(label(s), def, validate)
	if err != nil {
		return err
	}
	if v = strings.TrimSpace(v); v != "" {
		env[s.Env] = v
	}
	return nil
}

// guide prints the step-by-step explanation of where to get a value.
func (w *wizard) guide(g *provider.Guide, prefill, env map[string]string) {
	if g == nil {
		return
	}
	lookup := func(k string) string {
		if v := env[k]; v != "" {
			return v
		}
		return prefill[k]
	}
	w.printf("\n  %s %s\n", w.c.BoldCyan("ℹ"), w.c.Bold(g.Title))
	for i, step := range g.Steps {
		w.printf("    %s %s\n", w.c.Cyan(fmt.Sprintf("%d.", i+1)), w.links(expandGuide(step, lookup)))
	}
	w.printf("\n")
}

// expandGuide replaces {ENV} and {ENV|fallback} with the values answered so
// far (without a trailing slash, so URLs can be joined).
func expandGuide(s string, lookup func(string) string) string {
	var b strings.Builder
	for {
		i := strings.IndexByte(s, '{')
		j := strings.IndexByte(s[i+1:], '}')
		if i < 0 || j < 0 {
			b.WriteString(s)
			return b.String()
		}
		name, fallback, _ := strings.Cut(s[i+1:i+1+j], "|")
		v := strings.TrimRight(strings.TrimSpace(lookup(name)), "/")
		if v == "" {
			v = fallback
		}
		b.WriteString(s[:i] + v)
		s = s[i+2+j:]
	}
}

// links highlights the URLs of a line of text.
func (w *wizard) links(s string) string {
	if !w.c.Enabled() {
		return s
	}
	words := strings.Split(s, " ")
	for i, word := range words {
		if strings.HasPrefix(word, "https://") {
			url := strings.TrimRight(word, ".,;:)")
			words[i] = w.c.Link(url) + word[len(url):]
		}
	}
	return strings.Join(words, " ")
}

func (w *wizard) askSecret(s provider.Setting, prefill, env map[string]string) error {
	if v, ok := w.o.set[s.SecretAlt]; ok {
		env[s.SecretAlt] = v
		w.status("! %s: definido inline via --set (prefira %s)", s.SecretAlt, s.Env)
		return nil
	}
	inline := prefill[s.SecretAlt]
	moveInline := false
	if inline != "" && prefill[s.Env] == "" {
		w.status("! A configuração atual tem %s escrito direto no arquivo de configuração.", s.SecretAlt)
		var err error
		if moveInline, err = w.p.Confirm("Mover o segredo para um arquivo só seu?", true); err != nil {
			return err
		}
		if !moveInline {
			env[s.SecretAlt] = inline
			return nil
		}
	}

	var path string
	if v, ok := w.o.set[s.Env]; ok {
		path = v
		w.p.answered(s.Label, v)
	} else {
		var err error
		if path, err = w.p.Input(label(s), w.defaultFor(s, prefill), required); err != nil {
			return err
		}
	}
	path = w.e.Expand(path)
	if path == "" {
		return fmt.Errorf("%s é obrigatório", s.Env)
	}
	env[s.Env] = path

	if _, err := settings.ReadSecretFile(path); err == nil {
		w.status("  ✓ arquivo encontrado (conteúdo não exibido)")
		return nil
	} else if exists(path) {
		w.status("  ! %s existe, mas não é utilizável: %v", path, err)
	}
	if !moveInline {
		w.guide(s.Guide, prefill, env)
	}
	if w.o.dryRun {
		w.note("  (dry-run) o arquivo seria criado")
		return nil
	}
	if moveInline {
		if err := WriteSecretFile(w.ctx, w.e, path, inline); err != nil {
			return err
		}
		w.status("  ✓ segredo movido para %s", path)
		return nil
	}
	if w.p.Yes {
		w.status("  ! %s não existe; crie-o antes de usar (o -check vai falhar até lá)", path)
		return nil
	}
	create, err := w.p.Confirm("  O arquivo não existe. Criar agora a partir da área de transferência?", true)
	if err != nil {
		return err
	}
	if !create {
		w.status("  ! crie %s antes de usar o servidor", path)
		return nil
	}
	for {
		if err := w.p.Wait("  Copie o token (Ctrl+C) e tecle Enter... "); err != nil {
			return err
		}
		tok, clear, err := ReadClipboard(w.ctx, w.e)
		if err == nil {
			err = checkToken(tok)
		}
		if err == nil {
			if err = WriteSecretFile(w.ctx, w.e, path, tok); err == nil {
				clear()
				w.status("  ✓ token gravado em %s (conteúdo não exibido); área de transferência limpa", path)
				return nil
			}
		}
		w.status("  ✗ %v", err)
		again, cerr := w.p.Confirm("  Tentar de novo?", true)
		if cerr != nil {
			return cerr
		}
		if !again {
			w.status("  ! crie %s antes de usar o servidor", path)
			return nil
		}
	}
}

// ---------- executable ----------

func (w *wizard) chooseCommand() (string, bool, error) {
	if w.o.noCopy || w.e.Exe == "" {
		return w.e.Exe, false, nil
	}
	w.section("Executável")
	dir := w.o.binDir
	if dir == "" {
		var err error
		if dir, err = w.p.Input("Pasta onde instalar o executável", w.e.DefaultBinDir(w.b.Name), required); err != nil {
			return "", false, err
		}
	}
	dst := filepath.Join(w.e.Expand(dir), w.e.ExeName(w.b.Name))
	return dst, !samePath(w.e, dst, w.e.Exe), nil
}

// copyExecutable copies src to dst. A running copy of dst (e.g. in use by
// Claude Desktop on Windows) cannot be overwritten, but it can be renamed.
func copyExecutable(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if exists(dst) {
		old := dst + ".old"
		_ = os.Remove(old)
		if err := os.Rename(dst, old); err != nil {
			return fmt.Errorf("não consegui substituir %s (feche os apps que usam o servidor): %w", dst, err)
		}
	}
	return atomicWrite(dst, data, 0o755)
}

// ---------- validation ----------

func (w *wizard) runCheck(command string, env map[string]string) error {
	owned := ownedEnv(w.b.Providers)
	var base []string
	if w.e.Environ != nil {
		for _, kv := range w.e.Environ() {
			k, _, _ := strings.Cut(kv, "=")
			if !owned[strings.ToUpper(k)] && !owned[k] {
				base = append(base, kv)
			}
		}
	}
	for _, k := range sortedKeys(env) {
		base = append(base, k+"="+env[k])
	}
	out, err := w.e.Run(w.ctx, Cmd{Name: command, Args: []string{"-check"}, Env: base})
	for _, line := range strings.Split(strings.TrimRight(out, "\r\n"), "\n") {
		if line != "" {
			w.status("  %s", strings.TrimRight(line, ""))
		}
	}
	return err
}

// ---------- install ----------

func (w *wizard) install() error {
	w.header("instalação")

	sel, err := w.chooseTargets()
	if err != nil {
		return err
	}

	prefill := map[string]string{}
	existing := w.findExisting(sel.targets, w.knownNames()...)
	for _, f := range existing { // prefer the current name over the legacy one
		if f.name == w.o.name && len(f.entry.Env) > 0 {
			prefill = f.entry.Env
			break
		}
	}
	if len(prefill) == 0 {
		for _, f := range existing {
			if len(f.entry.Env) > 0 {
				prefill = f.entry.Env
				break
			}
		}
	}
	if len(existing) > 0 {
		w.printf("\n%s\n", w.c.Yellow("Instalação existente encontrada:"))
		for _, f := range existing {
			w.printf("  %s %s em %s\n", w.c.Yellow("•"), w.c.Bold(fmt.Sprintf("%q", f.name)), f.t.Path)
		}
		if len(prefill) > 0 {
			w.note("Os valores atuais serão usados como padrão.")
		}
	}
	// don't mutate the entry read from disk
	pf := map[string]string{}
	for k, v := range prefill {
		pf[k] = v
	}

	p, comps, err := w.chooseComponents(pf)
	if err != nil {
		return err
	}
	env, err := w.collectSettings(comps, pf)
	if err != nil {
		return err
	}
	command, copyBin, err := w.chooseCommand()
	if err != nil {
		return err
	}
	spec := Spec{Name: w.o.name, Command: command, Env: env}

	// ----- summary -----
	w.section("Resumo")
	w.printf("%s", w.c.Field("Executável", w.c.Cyan(command)))
	if copyBin {
		w.printf("  %s", w.c.Dim("(copiado de "+w.e.Exe+")"))
	}
	var cn []string
	for _, c := range comps {
		cn = append(cn, c.Name)
	}
	w.printf("\n%s\n%s\n", w.c.Field(p.Name, w.c.Cyan(strings.Join(cn, ", "))), w.c.Dim("Variáveis:"))
	owned := ownedEnv(w.b.Providers)
	secretAlts := map[string]bool{}
	for _, c := range comps {
		for _, s := range c.Settings {
			if s.SecretAlt != "" {
				secretAlts[s.SecretAlt] = true
			}
		}
	}
	for _, k := range sortedKeys(env) {
		v := env[k]
		if secretAlts[k] || (!owned[k] && looksSecret(k)) {
			v = w.c.Dim("•••• (oculto)")
		}
		w.printf("  %s %s %s\n", w.c.Cyan(k), w.c.Dim("="), v)
	}
	w.printf("%s\n", w.c.Dim("Destinos:"))
	for _, t := range sel.targets {
		where := t.Path
		if where == "" {
			where = w.c.Dim("(só mostrar a configuração)")
		}
		state := ""
		for _, f := range existing {
			if f.t.Key() == t.Key() && f.name == spec.Name {
				state = w.c.Yellow(" — substitui a entrada existente")
			}
		}
		w.printf("  %s %s %s%s\n", w.c.Cyan("•"), w.c.Bold(t.Label()+":"), where, state)
	}

	if w.o.dryRun {
		w.section("Dry-run")
		o := Opts{DryRun: true, Out: w.out}
		for _, t := range sel.targets {
			w.status("→ %s", t.Label())
			if err := t.App.Install(w.ctx, w.e, t, spec, o); err != nil {
				w.status("  ✗ %v", err)
			}
			if t.Path != "" {
				w.printf("%s\n", snippet(t.Keys, spec.Name, t.App.Entry(spec)))
			}
		}
		w.printf("\n%s\n", w.c.Yellow("Dry-run: nada foi gravado."))
		return nil
	}

	ok, err := w.p.Confirm("\nAplicar?", true)
	if err != nil {
		return err
	}
	if !ok {
		return errAborted
	}

	w.section("Aplicando")
	if copyBin {
		if err := copyExecutable(w.e.Exe, command); err != nil {
			return err
		}
		w.status("✓ executável copiado para %s", command)
	}
	if !w.o.skipCheck {
		w.note("Validando a configuração (%s -check)...", filepath.Base(command))
		if err := w.runCheck(command, env); err != nil {
			w.status("✗ a validação falhou: %v", err)
			cont, cerr := w.p.Confirm("Gravar a configuração mesmo assim?", false)
			if cerr != nil {
				return cerr
			}
			if !cont {
				return errors.New("validação falhou; corrija e rode de novo (ou use --skip-check)")
			}
		} else {
			w.status("✓ configuração válida")
		}
	}

	o := Opts{Out: w.out}
	var failed []string
	for _, t := range sel.targets {
		w.printf("\n")
		w.status("→ %s", t.Label())
		replace := true
		for _, f := range existing {
			if f.t.Key() == t.Key() && f.name == spec.Name && !w.o.force {
				if replace, err = w.p.Confirm(fmt.Sprintf("  Já existe %q aqui. Substituir?", spec.Name), true); err != nil {
					return err
				}
			}
		}
		if !replace {
			w.note("  = mantido como estava")
			continue
		}
		if err := t.App.Install(w.ctx, w.e, t, spec, o); err != nil {
			w.status("  ✗ %v", err)
			if errors.Is(err, ErrNotJSON) {
				w.printf("  %s\n%s\n", w.c.Yellow("Adicione manualmente:"), snippet(t.Keys, spec.Name, t.App.Entry(spec)))
			}
			failed = append(failed, t.Label())
			continue
		}
		for _, f := range existing {
			if f.t.Key() == t.Key() && f.name == LegacyName && spec.Name != LegacyName {
				rm, err := w.p.Confirm(fmt.Sprintf("  Remover a entrada antiga %q (7pace-mcp)?", LegacyName), true)
				if err != nil {
					return err
				}
				if rm {
					if _, err := t.App.Uninstall(w.ctx, w.e, t, LegacyName, o); err != nil {
						w.status("  ✗ %v", err)
					}
				}
			}
		}
	}

	w.section("Próximos passos")
	for _, a := range sel.apps {
		w.printf("%s %s %s\n", w.c.Cyan("•"), w.c.Bold(a.Name()+":"), a.NextSteps())
	}
	if len(failed) > 0 {
		return fmt.Errorf("não foi possível configurar: %s", strings.Join(failed, ", "))
	}
	w.printf("\n%s\n", w.c.Mark("✓ "+w.c.Bold("Instalação concluída.")))
	return nil
}

func looksSecret(k string) bool {
	k = strings.ToUpper(k)
	if strings.HasSuffix(k, "_FILE") {
		return false
	}
	for _, s := range []string{"TOKEN", "PAT", "SECRET", "PASSWORD", "KEY"} {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

// ---------- uninstall ----------

func (w *wizard) uninstall() error {
	w.header("desinstalação")
	sel, err := w.chooseTargets()
	if err != nil {
		return err
	}
	existing := w.findExisting(sel.targets, w.knownNames()...)
	if len(existing) == 0 {
		w.printf("\n")
		w.status("! Nenhuma entrada %s encontrada nos destinos escolhidos.", strings.Join(quoteAll(w.knownNames()), " ou "))
		for _, t := range sel.targets {
			if t.Path == "" {
				_, _ = t.App.Uninstall(w.ctx, w.e, t, w.o.name, Opts{Out: w.out})
			}
		}
		return nil
	}
	o := Opts{DryRun: w.o.dryRun, Out: w.out}
	w.section("Removendo")
	for _, f := range existing {
		ok, err := w.p.Confirm(fmt.Sprintf("Remover %q de %s (%s)?", f.name, f.t.Label(), f.t.Path), true)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if _, err := f.t.App.Uninstall(w.ctx, w.e, f.t, f.name, o); err != nil {
			w.status("  ✗ %v", err)
		}
	}
	w.printf("\n")
	w.note("O executável e os arquivos de token não foram apagados; remova-os manualmente se quiser.")
	return nil
}

func quoteAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = fmt.Sprintf("%q", s)
	}
	return out
}

// ---------- detect ----------

func (w *wizard) detect() {
	w.printf("%s\n%s\n%s\n", w.c.Title(w.b.Name, w.b.Version, ""), w.c.Field("Sistema", w.e.Describe()), w.c.Field("Executável atual", w.e.Exe))
	names := []string{w.b.Name, LegacyName}
	for _, h := range Harnesses() {
		if h.ID() == "generic" {
			continue
		}
		w.section(h.Name())
		for _, a := range h.Apps() {
			if a.Detect(w.e) {
				w.status("  ✓ %s", w.c.Bold(a.Name()))
			} else {
				w.note("  – %s (não encontrado)", a.Name())
			}
			if _, ok := a.(claudeCode); ok {
				if cli := claudeCLI(w.e); cli != "" {
					w.printf("      %s\n", w.c.Field("CLI", cli))
				}
			}
			scopes := a.Scopes()
			if scopes == nil {
				scopes = []string{""}
			}
			for _, sc := range scopes {
				t := a.Target(w.e, sc)
				prefix := ""
				if sc != "" {
					prefix = w.c.Yellow(sc) + ": "
				}
				state := w.c.Dim("não existe")
				if exists(t.Path) {
					state = w.c.Dim("sem entradas do servidor")
					entries, err := ReadEntries(t.Path, t.Keys)
					if err != nil {
						state = w.c.Red("ilegível: " + err.Error())
					}
					var got []string
					for _, n := range names {
						if e, ok := entries[n]; ok {
							s := fmt.Sprintf("%q → %s", n, e.Command)
							if n == LegacyName {
								s += " (antiga)"
							}
							got = append(got, s)
						}
					}
					if len(got) > 0 {
						state = w.c.Green(strings.Join(got, "; "))
					}
				}
				w.printf("      %s%s %s\n", prefix, t.Path, w.c.Dim("[")+state+w.c.Dim("]"))
				if t.Note != "" && sc == "" {
					w.note("      (%s)", t.Note)
				}
			}
		}
	}
	w.section("Outros clientes")
	w.printf("%s mostra a configuração pronta.\n", w.c.Cyan("`"+w.b.Name+" install --harness generic`"))
	w.section("Ferramentas de gestão disponíveis")
	for _, p := range w.b.Providers {
		var cs []string
		for _, c := range p.Components {
			cs = append(cs, c.Name)
		}
		w.printf("  %s %s %s %s\n", w.c.Cyan("•"), w.c.Bold(p.Name), w.c.Dim("("+p.ID+"):"), strings.Join(cs, ", "))
	}
}
