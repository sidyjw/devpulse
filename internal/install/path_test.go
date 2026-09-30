package install

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func withEnv(e *Env, vars map[string]string) {
	base := e.Getenv
	e.Getenv = func(k string) string {
		if v, ok := vars[k]; ok {
			return v
		}
		return base(k)
	}
}

func installArgs(e *Env, extra ...string) []string {
	return append([]string{"install", "--yes", "--harness", "claude", "--app", "desktop", "--skip-check",
		"--components", "boards", "--set", "AZURE_DEVOPS_ORG_URL=https://dev.azure.com/org",
		"--set", "AZURE_DEVOPS_PAT_FILE=" + filepath.Join(e.Home, "p")}, extra...)
}

func TestPathAddedToZshrcOnce(t *testing.T) {
	e, _ := fakeEnv(t, "linux")
	withEnv(e, map[string]string{"SHELL": "/usr/bin/zsh", "PATH": "/usr/bin"})
	rc := filepath.Join(e.Home, ".zshrc")
	writeFile(t, rc, "alias ll='ls -l'")
	for i := 0; i < 2; i++ {
		if code, out := runCLI(t, e, "", installArgs(e)...); code != 0 {
			t.Fatalf("code=%d\n%s", code, out)
		}
	}
	b, _ := os.ReadFile(rc)
	want := `export PATH="$HOME/.local/bin:$PATH"`
	if strings.Count(string(b), want) != 1 || !strings.HasPrefix(string(b), "alias ll='ls -l'\n\n# devpulse\n") {
		t.Errorf(".zshrc =\n%s", b)
	}
}

func TestPathSkipped(t *testing.T) {
	e, rec := fakeEnv(t, "linux")
	withEnv(e, map[string]string{"SHELL": "/bin/bash"})
	rc := filepath.Join(e.Home, ".bashrc")
	for _, extra := range [][]string{{"--no-path"}, {"--dry-run"}} {
		code, out := runCLI(t, e, "", installArgs(e, extra...)...)
		if code != 0 {
			t.Fatalf("%v: code=%d\n%s", extra, code, out)
		}
		if exists(rc) {
			t.Errorf("%v não deveria mexer no .bashrc", extra)
		}
		if extra[0] == "--dry-run" && !strings.Contains(out, "PATH") {
			t.Errorf("dry-run deveria mostrar a mudança no PATH:\n%s", out)
		}
	}
	// already in the PATH: nothing to ask or do (a Windows temp dir has a
	// ":" and cannot be in a Unix-style PATH, see TestInPath)
	if runtime.GOOS != "windows" {
		withEnv(e, map[string]string{"PATH": "/usr/bin:" + filepath.Join(e.Home, ".local", "bin") + "/"})
		if code, out := runCLI(t, e, "", installArgs(e)...); code != 0 || exists(rc) || strings.Contains(out, "Adicionar") {
			t.Errorf("pasta já no PATH (%d):\n%s", code, out)
		}
	}
	if len(rec.named("powershell.exe")) != 0 {
		t.Error("não deveria chamar o PowerShell no Linux")
	}
	// unknown shell: only the instruction
	withEnv(e, map[string]string{"SHELL": "/bin/tcsh", "PATH": "/usr/bin"})
	if code, out := runCLI(t, e, "", installArgs(e)...); code != 0 || !strings.Contains(out, "não está no PATH") {
		t.Errorf("shell desconhecido (%d):\n%s", code, out)
	}
}

func TestPathWindowsUsesUserEnvironment(t *testing.T) {
	e, rec := fakeEnv(t, "windows")
	withEnv(e, map[string]string{"PATH": `C:\Windows`})
	if code, out := runCLI(t, e, "", installArgs(e)...); code != 0 {
		t.Fatalf("code=%d\n%s", code, out)
	}
	calls := rec.named("powershell.exe")
	if len(calls) != 1 {
		t.Fatalf("chamadas = %+v", rec.calls)
	}
	c := calls[0]
	script := c.Args[len(c.Args)-1]
	dir := filepath.Join(e.LocalAppData, "Programs", "devpulse")
	if !strings.Contains(script, "SetValue('Path'") || strings.Contains(script, dir) || strings.Contains(script, "setx") {
		t.Errorf("script = %s", script)
	}
	if !strings.Contains(strings.Join(c.Env, "\n"), pathDirEnv+"="+dir) {
		t.Errorf("a pasta deveria ir por variável de ambiente: %v", c.Env)
	}
}

func TestInPath(t *testing.T) {
	e, _ := fakeEnv(t, "windows")
	withEnv(e, map[string]string{"PATH": `C:\Windows;c:\users\me\apps\DevPulse\;`})
	if !inPath(e, `C:\Users\me\Apps\devpulse`) || inPath(e, `C:\Users\me\Apps`) {
		t.Error("Windows: comparação deveria ignorar maiúsculas e a barra final")
	}
	e.GOOS = "linux"
	withEnv(e, map[string]string{"PATH": "/usr/bin:/home/me/.local/bin/"})
	if !inPath(e, "/home/me/.local/bin") || inPath(e, "/home/me/.Local/bin") {
		t.Error("Linux: comparação deveria ignorar a barra final e respeitar maiúsculas")
	}
}

func TestPlanPathQuoting(t *testing.T) {
	e, _ := fakeEnv(t, "darwin")
	withEnv(e, map[string]string{"SHELL": "/bin/bash"})
	pp := planPath(e, `/opt/my "tools"/$bin`)
	if pp.file != filepath.Join(e.Home, ".bash_profile") || pp.line != `export PATH="/opt/my \"tools\"/\$bin:$PATH"` {
		t.Errorf("plan = %+v", pp)
	}
	withEnv(e, map[string]string{"SHELL": "/usr/local/bin/fish"})
	if pp := planPath(e, filepath.Join(e.Home, "bin")); !strings.HasSuffix(pp.file, filepath.Join("fish", "conf.d", "devpulse.fish")) || pp.line != `fish_add_path -g "$HOME/bin"` {
		t.Errorf("fish = %+v", pp)
	}
}
