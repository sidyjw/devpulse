package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sidyjw/devpulse/internal/release"
)

// maxNotesLines limits how much of the release notes update prints.
const maxNotesLines = 12

// installed finds every entry of the server (current or legacy name) in the
// configuration files of the known harnesses, in every scope.
func (w *wizard) installed() []found {
	var ts []Target
	for _, h := range Harnesses() {
		for _, a := range h.Apps() {
			scopes := a.Scopes()
			if scopes == nil {
				scopes = []string{""}
			}
			for _, sc := range scopes {
				if t := a.Target(w.e, sc); t.Path != "" && exists(t.Path) {
					ts = append(ts, t)
				}
			}
		}
	}
	return w.findExisting(ts, w.knownNames()...)
}

// updateTarget is one executable to replace and the apps that use it.
type updateTarget struct {
	path string
	apps []App
}

// updateTargets lists the executables used by the current entries plus the
// running one, without duplicates.
func (w *wizard) updateTargets(entries []found) []*updateTarget {
	var out []*updateTarget
	add := func(p string, a App) {
		if p == "" || !exists(p) {
			return
		}
		for _, t := range out {
			if samePath(w.e, t.path, p) {
				if a != nil {
					t.apps = append(t.apps, a)
				}
				return
			}
		}
		t := &updateTarget{path: p}
		if a != nil {
			t.apps = []App{a}
		}
		out = append(out, t)
	}
	for _, f := range entries {
		if f.name == w.o.name {
			add(f.entry.Command, f.t.App)
		}
	}
	add(w.e.Exe, nil)
	return out
}

func (w *wizard) update() error {
	w.header("atualização")
	if w.e.Releases == nil {
		return errors.New("atualização indisponível nesta compilação")
	}

	var rel *release.Release
	var err error
	w.note("Consultando as releases de github.com/%s...", release.Repo)
	if w.o.version != "" {
		rel, err = w.e.Releases.Get(w.ctx, w.o.version)
	} else {
		rel, err = w.e.Releases.Latest(w.ctx)
	}
	if err != nil {
		return err
	}
	which := "Última versão"
	if w.o.version != "" {
		which = "Versão pedida"
	}
	w.printf("%s\n%s\n", w.c.Field("Versão instalada", w.b.Version), w.c.Field(which, w.c.Cyan(rel.Version)))

	cmp, cerr := release.Compare(rel.Version, w.b.Version)
	switch {
	case w.o.force:
	case cerr != nil:
		w.printf("\n")
		w.status("! Esta é uma compilação local (versão %q); não dá para comparar com as releases.", w.b.Version)
		w.note("Use --force para trocar pelo binário da release %s.", rel.Tag)
		return nil
	case cmp == 0:
		w.printf("\n")
		w.status("✓ Você já está na versão %s.", rel.Version)
		return nil
	case cmp < 0 && w.o.version == "":
		w.printf("\n")
		w.status("✓ Sua versão (%s) é mais nova que a última publicada.", w.b.Version)
		return nil
	}

	if notes := notesPreview(rel.Notes); notes != "" {
		w.section("Novidades de " + rel.Tag)
		w.printf("%s\n", notes)
	}
	if w.o.checkOnly {
		w.printf("\n")
		w.status("→ Versão nova disponível. Para atualizar: %s update", w.b.Name)
		return nil
	}

	entries := w.installed()
	targets := w.updateTargets(entries)
	for _, f := range entries {
		if f.name != w.o.name && isLegacy(f.name) {
			w.status("! %q em %s é de uma versão antiga; rode `%s install` para migrá-la.", f.name, f.t.Path, w.b.Name)
		}
	}
	if len(targets) == 0 {
		return errors.New("nenhum executável instalado encontrado; rode `" + w.b.Name + " install`")
	}
	w.section("Executáveis a atualizar")
	for _, t := range targets {
		var names []string
		for _, a := range t.apps {
			names = append(names, a.Name())
		}
		used := w.c.Dim("(este executável)")
		if len(names) > 0 {
			used = w.c.Dim("(usado por " + strings.Join(dedup(names), ", ") + ")")
		}
		w.printf("  %s %s %s\n", w.c.Cyan("•"), t.path, used)
	}
	if w.o.dryRun {
		w.printf("\n%s\n", w.c.Yellow("Dry-run: nada foi baixado nem gravado."))
		return nil
	}
	ok, err := w.p.Confirm(fmt.Sprintf("\nAtualizar para %s?", rel.Version), true)
	if err != nil {
		return err
	}
	if !ok {
		return errAborted
	}

	w.section("Atualizando")
	data, err := w.e.Releases.Executable(w.ctx, rel, w.b.Name, w.e.GOOS, w.e.GOARCH)
	if err != nil {
		return err
	}
	w.status("✓ %s baixado e conferido com o %s", release.ArchiveName(w.b.Name, rel.Version, w.e.GOOS, w.e.GOARCH), release.SumsFile)

	var failed []string
	var apps []App
	for _, t := range targets {
		if err := w.replaceExecutable(t.path, data, rel.Version); err != nil {
			w.status("✗ %s: %v", t.path, err)
			failed = append(failed, t.path)
			continue
		}
		w.status("✓ %s atualizado", t.path)
		apps = append(apps, t.apps...)
	}

	if len(apps) > 0 {
		w.section("Próximos passos")
		seen := map[string]bool{}
		for _, a := range apps {
			if !seen[a.ID()] {
				seen[a.ID()] = true
				w.printf("%s %s %s\n", w.c.Cyan("•"), w.c.Bold(a.Name()+":"), a.NextSteps())
			}
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("não foi possível atualizar: %s", strings.Join(failed, ", "))
	}
	w.printf("\n%s\n", w.c.Mark("✓ "+w.c.Bold("Atualizado para "+rel.Version+".")))
	return nil
}

// replaceExecutable writes the new executable next to dst, checks that it
// runs and only then puts it in place (the old one is kept as dst.old).
func (w *wizard) replaceExecutable(dst string, data []byte, version string) error {
	ext := filepath.Ext(dst)
	tmp := strings.TrimSuffix(dst, ext) + ".new" + ext
	if err := atomicWrite(tmp, data, 0o755); err != nil {
		return err
	}
	out, err := w.e.Run(w.ctx, Cmd{Name: tmp, Args: []string{"-version"}})
	if err != nil || !strings.Contains(out, version) {
		os.Remove(tmp)
		if err == nil {
			err = fmt.Errorf("o novo executável informou %q", strings.TrimSpace(out))
		}
		return fmt.Errorf("o novo executável não rodou: %w", err)
	}
	if err := moveAside(dst); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Rename(dst+".old", dst) // put the old one back
		os.Remove(tmp)
		return err
	}
	return nil
}

// notesPreview returns the first lines of the release notes.
func notesPreview(notes string) string {
	var lines []string
	for _, l := range strings.Split(strings.ReplaceAll(notes, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if len(lines) == maxNotesLines {
			lines = append(lines, "  …")
			break
		}
		lines = append(lines, "  "+l)
	}
	return strings.Join(lines, "\n")
}

func dedup(ss []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
