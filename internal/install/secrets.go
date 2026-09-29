package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Tokens go from the clipboard straight to a file readable only by the
// user; they are never echoed, logged or passed on a command line.

type clipCmd struct {
	read  Cmd
	clear Cmd
}

func clipboardCmds(e *Env) []clipCmd {
	ps := func(exe string) clipCmd {
		return clipCmd{
			read:  Cmd{Name: exe, Args: []string{"-NoProfile", "-NonInteractive", "-Command", "Get-Clipboard -Raw"}},
			clear: Cmd{Name: exe, Args: []string{"-NoProfile", "-NonInteractive", "-Command", "Set-Clipboard -Value ' '"}},
		}
	}
	switch e.GOOS {
	case "windows":
		return []clipCmd{ps("powershell.exe")}
	case "darwin":
		return []clipCmd{{read: Cmd{Name: "pbpaste"}, clear: Cmd{Name: "pbcopy", Stdin: " "}}}
	default:
		cs := []clipCmd{
			{read: Cmd{Name: "wl-paste", Args: []string{"--no-newline"}}, clear: Cmd{Name: "wl-copy", Args: []string{"--clear"}}},
			{read: Cmd{Name: "xclip", Args: []string{"-selection", "clipboard", "-o"}}, clear: Cmd{Name: "xclip", Args: []string{"-selection", "clipboard", "-i"}, Stdin: " "}},
			{read: Cmd{Name: "xsel", Args: []string{"--clipboard", "--output"}}, clear: Cmd{Name: "xsel", Args: []string{"--clipboard", "--clear"}}},
		}
		if e.WSL {
			cs = append(cs, ps("powershell.exe"))
		}
		return cs
	}
}

// ReadClipboard returns the trimmed clipboard text and a function that
// clears the clipboard.
func ReadClipboard(ctx context.Context, e *Env) (string, func(), error) {
	var tried []string
	for _, c := range clipboardCmds(e) {
		if e.LookPath != nil {
			if _, err := e.LookPath(c.read.Name); err != nil {
				tried = append(tried, c.read.Name)
				continue
			}
		}
		out, err := e.Run(ctx, c.read)
		if err != nil {
			return "", nil, fmt.Errorf("ler a área de transferência com %s: %w", c.read.Name, err)
		}
		clear := func() { _, _ = e.Run(context.Background(), c.clear) }
		return strings.TrimSpace(out), clear, nil
	}
	return "", nil, fmt.Errorf("nenhum utilitário de área de transferência encontrado (tentei: %s)", strings.Join(tried, ", "))
}

// checkToken rejects clipboard content that obviously is not a token.
func checkToken(s string) error {
	switch {
	case s == "":
		return errors.New("a área de transferência está vazia")
	case strings.ContainsAny(s, "\r\n"):
		return errors.New("a área de transferência tem mais de uma linha; copie só o token")
	case len(s) > 4096:
		return errors.New("o conteúdo copiado é grande demais para ser um token")
	case strings.ContainsAny(s, " \t"):
		return errors.New("o conteúdo copiado tem espaços; copie só o token")
	}
	return nil
}

// WriteSecretFile writes value to path so that only the current user can
// read it (chmod 600; icacls on Windows).
func WriteSecretFile(ctx context.Context, e *Env, path, value string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := atomicWrite(path, []byte(value), 0o600); err != nil {
		return fmt.Errorf("gravar %s: %w", path, err)
	}
	if e.GOOS == "windows" {
		user := ""
		if e.Getenv != nil {
			user = e.Getenv("USERNAME")
		}
		if user == "" {
			return fmt.Errorf("gravado em %s, mas USERNAME não está definido para restringir as permissões", path)
		}
		if _, err := e.Run(ctx, Cmd{Name: "icacls", Args: []string{path, "/inheritance:r", "/grant:r", user + ":(F)"}}); err != nil {
			return fmt.Errorf("gravado em %s, mas icacls falhou: %w", path, err)
		}
	}
	return nil
}
