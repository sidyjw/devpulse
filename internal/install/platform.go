package install

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/sidyjw/devpulse/internal/release"
)

// Cmd is an external command run by the installer.
type Cmd struct {
	Name  string
	Args  []string
	Env   []string // nil inherits the current environment
	Dir   string
	Stdin string
}

// Env is everything the installer knows about the machine. Tests build one
// pointing at temporary directories with fake LookPath and Run.
type Env struct {
	GOOS, GOARCH string
	WSL          bool
	Home         string
	AppData      string // Windows %APPDATA%
	LocalAppData string // Windows %LOCALAPPDATA%
	XDGConfig    string // $XDG_CONFIG_HOME or ~/.config
	Cwd          string
	Exe          string // this executable

	Getenv   func(string) string
	Environ  func() []string
	LookPath func(string) (string, error)
	// Run executes a command and returns its stdout.
	Run func(ctx context.Context, c Cmd) (string, error)
	// Releases finds and downloads the published versions (update).
	Releases *release.Client
}

// RealEnv inspects the running system.
func RealEnv() *Env {
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	exe, _ := os.Executable()
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	e := &Env{
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Home: home, Cwd: cwd, Exe: exe,
		AppData:      os.Getenv("APPDATA"),
		LocalAppData: os.Getenv("LOCALAPPDATA"),
		XDGConfig:    os.Getenv("XDG_CONFIG_HOME"),
		Getenv:       os.Getenv,
		Environ:      os.Environ,
		LookPath:     exec.LookPath,
		Run:          runCmd,
		Releases:     release.New(),
	}
	if e.XDGConfig == "" && home != "" {
		e.XDGConfig = filepath.Join(home, ".config")
	}
	if runtime.GOOS == "linux" {
		if b, err := os.ReadFile("/proc/version"); err == nil && bytes.Contains(bytes.ToLower(b), []byte("microsoft")) {
			e.WSL = true
		}
	}
	return e
}

func runCmd(ctx context.Context, c Cmd) (string, error) {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Env = c.Env
	cmd.Dir = c.Dir
	if c.Stdin != "" {
		cmd.Stdin = strings.NewReader(c.Stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		if msg != "" {
			return out.String(), fmt.Errorf("%w: %s", err, msg)
		}
		return out.String(), err
	}
	return out.String(), nil
}

// Describe returns e.g. "Windows (amd64)".
func (e *Env) Describe() string {
	name := map[string]string{"windows": "Windows", "darwin": "macOS", "linux": "Linux"}[e.GOOS]
	if name == "" {
		name = e.GOOS
	}
	if e.WSL {
		name += "/WSL"
	}
	return fmt.Sprintf("%s (%s)", name, e.GOARCH)
}

// ExeName adds ".exe" on Windows.
func (e *Env) ExeName(base string) string {
	if e.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}

// DefaultBinDir is where the executable is copied by default.
func (e *Env) DefaultBinDir(name string) string {
	if e.GOOS == "windows" && e.LocalAppData != "" {
		return filepath.Join(e.LocalAppData, "Programs", name)
	}
	return filepath.Join(e.Home, ".local", "bin")
}

// Expand turns a leading "~" into the home directory and makes the path absolute.
func (e *Env) Expand(p string) string {
	p = strings.TrimSpace(p)
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		p = filepath.Join(e.Home, p[1:])
	}
	if p != "" && !filepath.IsAbs(p) && e.Cwd != "" {
		p = filepath.Join(e.Cwd, p)
	}
	return p
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func samePath(e *Env, a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if e.GOOS == "windows" || e.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

var errAborted = errors.New("cancelado")
