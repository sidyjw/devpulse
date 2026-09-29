package ui

import (
	"bytes"
	"strings"
	"testing"
)

func TestForNonTerminalIsPlain(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("FORCE_COLOR", "")
	t.Setenv("CLICOLOR_FORCE", "")
	c := For(&bytes.Buffer{})
	if c.Enabled() || c.Red("x") != "x" || c.Mark("  ✗ falhou") != "  ✗ falhou" || c.Section("Resumo") != "== Resumo ==" {
		t.Fatal("um buffer não deveria receber cores")
	}
}

func TestEnvOverrides(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("CLICOLOR_FORCE", "")
	t.Setenv("FORCE_COLOR", "1")
	if !For(&bytes.Buffer{}).Enabled() {
		t.Fatal("FORCE_COLOR deveria ligar as cores")
	}
	t.Setenv("NO_COLOR", "1")
	if For(&bytes.Buffer{}).Enabled() {
		t.Fatal("NO_COLOR deveria vencer FORCE_COLOR")
	}
}

func TestMark(t *testing.T) {
	c := Palette{on: true}
	for _, tc := range []struct{ in, code string }{
		{"  ✓ ok", "\x1b[1;32m✓"},
		{"✗ falhou", "\x1b[1;31m✗"},
		{"  ! cuidado", "\x1b[1;33m!"},
		{"  = igual", "\x1b[2m="},
		{"→ passo", "\x1b[1;36m→"},
	} {
		got := c.Mark(tc.in)
		if !strings.Contains(got, tc.code) || !strings.Contains(got, "\x1b[0m") {
			t.Errorf("Mark(%q) = %q", tc.in, got)
		}
	}
	for _, in := range []string{"texto comum", "  !importante", ""} {
		if got := c.Mark(in); got != in {
			t.Errorf("Mark(%q) = %q, esperava sem alteração", in, got)
		}
	}
}
