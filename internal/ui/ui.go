// Package ui adds color to the terminal output of the CLI (installer and
// -check) using plain ANSI escape codes. Colors are enabled only when the
// writer is a terminal; NO_COLOR turns them off and FORCE_COLOR (or
// CLICOLOR_FORCE) turns them on. Standard library only.
package ui

import (
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

// Palette paints strings; the zero value paints nothing.
type Palette struct{ on bool }

// For returns the palette to use when writing to w.
func For(w io.Writer) Palette {
	if os.Getenv("NO_COLOR") != "" {
		return Palette{}
	}
	f, isFile := w.(*os.File)
	if forced() {
		if isFile {
			EnableVT(f)
		}
		return Palette{on: true}
	}
	if !isFile || os.Getenv("TERM") == "dumb" {
		return Palette{}
	}
	return Palette{on: colorTerminal(f)}
}

// VisibleLen is the number of columns s takes, ignoring escape codes.
func VisibleLen(s string) int {
	n := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			i += 2
			for i < len(s) && (s[i] < 0x40 || s[i] > 0x7e) {
				i++
			}
			i++ // final byte
			continue
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
		n++
	}
	return n
}

// Truncate shortens plain text s to at most n columns, ending with "…".
func Truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

func forced() bool {
	for _, k := range []string{"FORCE_COLOR", "CLICOLOR_FORCE"} {
		if v := os.Getenv(k); v != "" && v != "0" && !strings.EqualFold(v, "false") {
			return true
		}
	}
	return false
}

// Enabled reports whether the palette emits escape codes.
func (p Palette) Enabled() bool { return p.on }

func (p Palette) paint(code, s string) string {
	if !p.on || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (p Palette) Bold(s string) string     { return p.paint("1", s) }
func (p Palette) Dim(s string) string      { return p.paint("2", s) }
func (p Palette) Red(s string) string      { return p.paint("31", s) }
func (p Palette) Green(s string) string    { return p.paint("32", s) }
func (p Palette) Yellow(s string) string   { return p.paint("33", s) }
func (p Palette) Magenta(s string) string  { return p.paint("35", s) }
func (p Palette) Cyan(s string) string     { return p.paint("36", s) }
func (p Palette) BoldRed(s string) string  { return p.paint("1;31", s) }
func (p Palette) BoldCyan(s string) string { return p.paint("1;36", s) }
func (p Palette) Link(s string) string     { return p.paint("4;36", s) }

// Title is the banner printed at the start of a command.
func (p Palette) Title(name, version, action string) string {
	s := p.paint("1;35", name)
	if version != "" {
		s += " " + p.Dim("v"+strings.TrimPrefix(version, "v"))
	}
	if action != "" {
		s += p.Dim(" — ") + p.Bold(action)
	}
	return s
}

// Field formats "label: value" with a discreet label.
func (p Palette) Field(label, value string) string {
	return p.Dim(label+":") + " " + value
}

const sectionWidth = 56

// Section is a section header (without the leading blank line).
func (p Palette) Section(title string) string {
	if !p.on {
		return "== " + title + " =="
	}
	n := sectionWidth - utf8.RuneCountInString(title) - 4
	if n < 3 {
		n = 3
	}
	return p.Cyan("──") + " " + p.BoldCyan(title) + " " + p.Cyan(strings.Repeat("─", n))
}

// Mark colors the status marker that starts line (after any indentation):
// ✓ success, ✗ error, ! warning, ~ would change (dry-run), = unchanged,
// → step. Lines without a marker are returned as they are.
func (p Palette) Mark(line string) string {
	if !p.on {
		return line
	}
	body := strings.TrimLeft(line, " ")
	indent := line[:len(line)-len(body)]
	r, size := utf8.DecodeRuneInString(body)
	rest := body[size:]
	if rest != "" && rest[0] != ' ' {
		return line // not a marker, e.g. "!important"
	}
	switch r {
	case '✓':
		return indent + p.paint("1;32", "✓") + rest
	case '✗':
		return indent + p.BoldRed("✗") + p.Red(rest)
	case '!':
		return indent + p.paint("1;33", "!") + p.Yellow(rest)
	case '~':
		return indent + p.paint("1;33", "~") + rest
	case '=':
		return indent + p.Dim("="+rest)
	case '→':
		return indent + p.BoldCyan("→"+rest)
	}
	return line
}
