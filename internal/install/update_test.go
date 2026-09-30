package install

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sidyjw/devpulse/internal/release/releasetest"
)

func runUpdate(t *testing.T, e *Env, version string, args ...string) (int, string) {
	t.Helper()
	b := testBuild
	b.Version = version
	var out bytes.Buffer
	code := run(context.Background(), append([]string{"update"}, args...), b, e, strings.NewReader(""), &out, &out)
	return code, out.String()
}

// updateEnv has Claude Desktop configured with an installed executable.
func updateEnv(t *testing.T) (*Env, *recorder, *releasetest.Fake, string) {
	t.Helper()
	e, rec := fakeEnv(t, "windows")
	bin := filepath.Join(e.LocalAppData, "Programs", "devpulse", "devpulse.exe")
	writeFile(t, bin, "binário antigo")
	writeFile(t, filepath.Join(e.AppData, "Claude", "claude_desktop_config.json"),
		`{"mcpServers":{"devpulse":{"command":`+jsonStr(bin)+`},"7pace":{"command":"C:\\old\\7pace.exe"}}}`)
	f := &releasetest.Fake{Name: "devpulse", Version: "0.2.0", Notes: "### Adicionado\n\n- devpulse update", Exe: []byte("binário novo")}
	e.Releases = f.Start(t)
	rec.reply = func(c Cmd) (string, error) { return "devpulse 0.2.0\n", nil }
	return e, rec, f, bin
}

func TestUpdateReplacesInstalledExecutables(t *testing.T) {
	e, rec, _, bin := updateEnv(t)
	code, out := runUpdate(t, e, "0.1.0", "--yes")
	if code != 0 {
		t.Fatalf("code=%d\n%s", code, out)
	}
	for _, p := range []string{bin, e.Exe} {
		if b, _ := os.ReadFile(p); string(b) != "binário novo" {
			t.Errorf("%s = %q", p, b)
		}
	}
	if b, _ := os.ReadFile(bin + ".old"); string(b) != "binário antigo" {
		t.Errorf("o executável anterior deveria ficar em .old: %q", b)
	}
	if exists(strings.TrimSuffix(bin, ".exe") + ".new.exe") {
		t.Error("o arquivo temporário .new deveria ter sido renomeado")
	}
	// the new binary was run (-version) before replacing each file
	if len(rec.calls) != 2 || rec.calls[0].Args[0] != "-version" || !strings.HasSuffix(rec.calls[0].Name, ".new.exe") {
		t.Errorf("chamadas = %+v", rec.calls)
	}
	for _, want := range []string{"devpulse update", "Claude Desktop", "versão antiga", "Atualizado para 0.2.0"} {
		if !strings.Contains(out, want) {
			t.Errorf("saída sem %q:\n%s", want, out)
		}
	}
}

func TestUpdateCheckAndDryRunWriteNothing(t *testing.T) {
	for _, flag := range []string{"--check", "--dry-run"} {
		e, rec, f, bin := updateEnv(t)
		code, out := runUpdate(t, e, "0.1.0", "--yes", flag)
		if code != 0 {
			t.Fatalf("%s: code=%d\n%s", flag, code, out)
		}
		if b, _ := os.ReadFile(bin); string(b) != "binário antigo" || exists(bin+".old") || len(rec.calls) != 0 {
			t.Errorf("%s gravou ou executou algo", flag)
		}
		for _, r := range f.Requests() {
			if strings.HasPrefix(r, "/download/") {
				t.Errorf("%s baixou %s", flag, r)
			}
		}
		if !strings.Contains(out, "0.2.0") {
			t.Errorf("%s: saída:\n%s", flag, out)
		}
	}
}

func TestUpdateUpToDateAndDevBuild(t *testing.T) {
	e, _, _, bin := updateEnv(t)
	for _, v := range []string{"0.2.0", "0.3.0-rc.1", "dev"} {
		code, out := runUpdate(t, e, v, "--yes")
		if code != 0 {
			t.Errorf("%s: code=%d\n%s", v, code, out)
		}
		if b, _ := os.ReadFile(bin); string(b) != "binário antigo" {
			t.Errorf("%s: não deveria atualizar", v)
		}
	}
	// --version pins (also to go back); a missing version fails
	if code, out := runUpdate(t, e, "0.3.0", "--yes", "--version", "v0.2.0"); code != 0 || !strings.Contains(out, "Versão pedida") {
		t.Errorf("--version: code=%d\n%s", code, out)
	}
	if code, _ := runUpdate(t, e, "0.1.0", "--yes", "--version", "9.9.9"); code == 0 {
		t.Error("versão inexistente deveria falhar")
	}
}

func TestUpdateKeepsOldWhenNewDoesNotRun(t *testing.T) {
	e, rec, _, bin := updateEnv(t)
	rec.reply = func(c Cmd) (string, error) { return "", os.ErrPermission }
	code, out := runUpdate(t, e, "0.1.0", "--yes")
	if code == 0 {
		t.Fatalf("deveria falhar:\n%s", out)
	}
	if b, _ := os.ReadFile(bin); string(b) != "binário antigo" {
		t.Errorf("o executável antigo deveria continuar: %q", b)
	}
	if exists(strings.TrimSuffix(bin, ".exe") + ".new.exe") {
		t.Error("o .new deveria ser apagado")
	}
}
