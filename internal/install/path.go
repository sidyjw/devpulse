package install

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// pathDirEnv carries the folder to the PowerShell script, so it is never
// interpolated into the command line.
const pathDirEnv = "DEVPULSE_PATH_DIR"

// Adds $env:DEVPULSE_PATH_DIR to the user's PATH (HKCU\Environment), unless
// it is there. The raw value is kept (entries like %USERPROFILE%\bin stay
// unexpanded) and written back as REG_EXPAND_SZ; setx is not used because it
// truncates the value at 1024 characters. Clearing a variable at the end
// makes Windows broadcast the change, so new terminals see it.
const psAddToPath = `$d = $env:` + pathDirEnv + `
$k = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment')
$present = $false
try {
  $p = [string]$k.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
  $parts = @($p -split ';' | Where-Object { $_ -ne '' })
  $expanded = $parts | ForEach-Object { [Environment]::ExpandEnvironmentVariables($_).TrimEnd('\') }
  $present = $expanded -contains $d.TrimEnd('\')
  if (-not $present) {
    $k.SetValue('Path', (($parts + $d) -join ';'), [Microsoft.Win32.RegistryValueKind]::ExpandString)
  }
} finally { $k.Close() }
if ($present) { 'present' } else {
  [Environment]::SetEnvironmentVariable('` + pathDirEnv + `', $null, 'User')
  'added'
}`

// pathPlan says how the folder of the executable gets into the PATH.
type pathPlan struct {
	dir  string
	file string // shell profile to append to (empty on Windows)
	line string // what is appended to file
}

// inPath reports whether dir is in the PATH of the current process.
func inPath(e *Env, dir string) bool {
	sep := ":"
	if e.GOOS == "windows" {
		sep = ";"
	}
	for _, p := range strings.Split(e.Getenv("PATH"), sep) {
		if p = strings.TrimSpace(p); p != "" && samePath(e, strings.TrimRight(p, `/\`), strings.TrimRight(dir, `/\`)) {
			return true
		}
	}
	return false
}

// planPath returns how dir would be added to the PATH, or nil when the
// shell is unknown (the user gets the instruction instead).
func planPath(e *Env, dir string) *pathPlan {
	if e.GOOS == "windows" {
		return &pathPlan{dir: dir}
	}
	shown := dir
	if rel, err := filepath.Rel(e.Home, dir); err == nil && !strings.HasPrefix(rel, "..") {
		shown = "$HOME/" + filepath.ToSlash(rel)
	}
	quoted := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`").Replace(shown)
	if !strings.HasPrefix(shown, "$HOME/") {
		quoted = strings.ReplaceAll(quoted, "$", `\$`)
	}
	switch filepath.Base(e.Getenv("SHELL")) {
	case "zsh":
		home := e.Getenv("ZDOTDIR")
		if home == "" {
			home = e.Home
		}
		return &pathPlan{dir: dir, file: filepath.Join(home, ".zshrc"), line: `export PATH="` + quoted + `:$PATH"`}
	case "bash":
		f := ".bashrc"
		if e.GOOS == "darwin" {
			f = ".bash_profile"
		}
		return &pathPlan{dir: dir, file: filepath.Join(e.Home, f), line: `export PATH="` + quoted + `:$PATH"`}
	case "fish":
		return &pathPlan{dir: dir, file: filepath.Join(e.XDGConfig, "fish", "conf.d", "devpulse.fish"), line: `fish_add_path -g "` + quoted + `"`}
	}
	return nil
}

// describe is the line shown in the summary.
func (pp *pathPlan) describe() string {
	if pp.file == "" {
		return "adiciona " + pp.dir + " ao PATH do usuário"
	}
	return "acrescenta `" + pp.line + "` em " + pp.file
}

// applyPath adds the folder to the PATH. It is idempotent.
func (w *wizard) applyPath(pp *pathPlan) error {
	if pp.file == "" {
		var env []string
		if w.e.Environ != nil {
			env = w.e.Environ()
		}
		out, err := w.e.Run(w.ctx, Cmd{
			Name: "powershell.exe",
			Args: []string{"-NoProfile", "-NonInteractive", "-Command", psAddToPath},
			Env:  append(env, pathDirEnv+"="+pp.dir),
		})
		if err != nil {
			return err
		}
		if strings.TrimSpace(out) == "present" {
			w.status("✓ %s já está no PATH do usuário", pp.dir)
		} else {
			w.status("✓ %s adicionado ao PATH do usuário", pp.dir)
		}
		return nil
	}
	b, err := os.ReadFile(pp.file)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if strings.Contains(string(b), pp.line) {
		w.status("✓ %s já tem a pasta no PATH", pp.file)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(pp.file), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(pp.file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	prefix := "\n"
	if len(b) > 0 && !strings.HasSuffix(string(b), "\n") {
		prefix = "\n\n"
	} else if len(b) == 0 {
		prefix = ""
	}
	_, err = fmt.Fprintf(f, "%s# %s\n%s\n", prefix, w.b.Name, pp.line)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	w.status("✓ PATH atualizado em %s", pp.file)
	return nil
}

// choosePath asks whether to add the folder of the executable to the PATH.
// It returns nil when nothing is to be done.
func (w *wizard) choosePath(command string) (*pathPlan, error) {
	dir := filepath.Dir(command)
	if w.o.noPath || w.o.noCopy || inPath(w.e, dir) {
		return nil, nil
	}
	pp := planPath(w.e, dir)
	if pp == nil {
		w.status("! %s não está no PATH; adicione-a para rodar `%s` de qualquer pasta.", dir, w.b.Name)
		return nil, nil
	}
	ok, err := w.p.Confirm(fmt.Sprintf("Adicionar %s ao PATH (para rodar `%s update` de qualquer pasta)?", dir, w.b.Name), true)
	if err != nil || !ok {
		return nil, err
	}
	return pp, nil
}
