package install

import (
	"fmt"
	"strings"

	"github.com/sidyjw/devpulse/internal/ui"
)

// Arrow-key menus, used when stdin and stdout are a terminal. Each question
// is drawn as a block that is redrawn in place on every key and, once
// answered, collapsed into a single "? question answer" line. When the
// terminal can't enter raw mode the Prompter falls back to typed answers.

// keyboard is how the Prompter reaches the terminal (replaced in tests).
type keyboard struct {
	raw   func() (restore func(), err error)
	width func() int
}

const (
	hideCursor = "\x1b[?25l"
	showCursor = "\x1b[?25h"
)

type key int

const (
	keyOther key = iota
	keyUp
	keyDown
	keyLeft
	keyRight
	keyHome
	keyEnd
	keyEnter
	keySpace
	keyTab
	keyCancel
)

// readKey reads one key press in raw mode.
func (p *Prompter) readKey() (key, rune, error) {
	r, _, err := p.in.ReadRune()
	if err != nil {
		return keyOther, 0, err
	}
	switch r {
	case '\r', '\n':
		return keyEnter, r, nil
	case ' ':
		return keySpace, r, nil
	case '\t':
		return keyTab, r, nil
	case 3, 4: // Ctrl+C, Ctrl+D
		return keyCancel, r, nil
	case 'k':
		return keyUp, r, nil
	case 'j':
		return keyDown, r, nil
	case 0x1b:
		// A lone Esc is ignored: if an arrow's sequence arrived split, the
		// installer must not be cancelled by accident.
		if p.in.Buffered() == 0 {
			return keyOther, r, nil
		}
		if b, _ := p.in.ReadByte(); b != '[' && b != 'O' {
			return keyOther, r, nil
		}
		for { // parameters, then the final byte
			b, err := p.in.ReadByte()
			if err != nil {
				return keyOther, 0, err
			}
			if b < 0x40 || b > 0x7e {
				continue
			}
			switch b {
			case 'A':
				return keyUp, 0, nil
			case 'B':
				return keyDown, 0, nil
			case 'C':
				return keyRight, 0, nil
			case 'D':
				return keyLeft, 0, nil
			case 'H':
				return keyHome, 0, nil
			case 'F':
				return keyEnd, 0, nil
			}
			return keyOther, 0, nil
		}
	}
	return keyOther, r, nil
}

// frame redraws a block of lines in place.
type frame struct {
	p     *Prompter
	width int
	rows  int // terminal rows taken by the last draw
}

func (f *frame) erase(b *strings.Builder) {
	if f.rows > 0 {
		fmt.Fprintf(b, "\r\x1b[%dA\x1b[J", f.rows)
		f.rows = 0
	}
}

func (f *frame) draw(lines []string) {
	var b strings.Builder
	f.erase(&b)
	for _, l := range lines {
		b.WriteString(l)
		b.WriteString("\n")
		f.rows++
		if n := ui.VisibleLen(l); f.width > 0 && n > f.width {
			f.rows += (n - 1) / f.width
		}
	}
	fmt.Fprint(f.p.out, b.String())
}

// finish replaces the block with its final line.
func (f *frame) finish(line string) {
	var b strings.Builder
	f.erase(&b)
	b.WriteString(line + "\n")
	fmt.Fprint(f.p.out, b.String())
}

// interactive runs loop with the terminal in raw mode. ok is false when raw
// mode is unavailable; the Prompter then stops trying and asks by typing.
func (p *Prompter) interactive(loop func(f *frame) error) (err error, ok bool) {
	restore, rerr := p.keys.raw()
	if rerr != nil {
		p.keys = nil
		return nil, false
	}
	defer restore()
	fmt.Fprint(p.out, hideCursor)
	defer fmt.Fprint(p.out, showCursor)
	f := &frame{p: p, width: p.keys.width()}
	return loop(f), true
}

// answer is the collapsed line of an answered question.
func (p *Prompter) answer(indent, title, value string) string {
	return indent + p.c.BoldCyan("?") + " " + p.c.Bold(title) + " " + p.c.Cyan(value)
}

func (p *Prompter) help(indent, s string) string {
	return indent + p.c.Dim(s)
}

// fit truncates the plain text s so that it fits after used columns.
func (f *frame) fit(used int, s string) string {
	if f.width <= 0 {
		return s
	}
	return ui.Truncate(s, f.width-1-used)
}

// optionLines renders the options of a menu; checked is nil for Select.
func (p *Prompter) optionLines(f *frame, opts []Option, cursor int, checked []bool) []string {
	lines := make([]string, 0, len(opts))
	for i, o := range opts {
		pointer := "  "
		if i == cursor {
			pointer = p.c.Cyan("❯") + " "
		}
		box, used := "", 4
		if checked != nil {
			box, used = p.c.Dim("○")+" ", 6
			if checked[i] {
				box = p.c.Green("●") + " "
			}
		}
		label := f.fit(used, o.Label)
		if i == cursor {
			label = p.c.BoldCyan(label)
		}
		hint := ""
		if o.Hint != "" {
			if room := f.width - 1 - used - ui.VisibleLen(o.Label); f.width <= 0 || room > 6 {
				hint = p.c.Dim(f.fit(used+ui.VisibleLen(o.Label), "  — "+o.Hint))
			}
		}
		lines = append(lines, "  "+pointer+box+label+hint)
	}
	return lines
}

func (p *Prompter) menuSelect(title string, opts []Option, def int) (int, error, bool) {
	cursor := def
	err, ok := p.interactive(func(f *frame) error {
		for {
			lines := []string{p.answer("", title, "")}
			lines = append(lines, p.optionLines(f, opts, cursor, nil)...)
			lines = append(lines, p.help("  ", f.fit(2, "↑/↓ mover · Enter confirmar · Ctrl+C cancelar")))
			f.draw(lines)
			k, r, err := p.readKey()
			if err != nil {
				f.finish(p.answer("", title, ""))
				return err
			}
			switch k {
			case keyUp, keyLeft:
				cursor = (cursor - 1 + len(opts)) % len(opts)
			case keyDown, keyRight, keyTab:
				cursor = (cursor + 1) % len(opts)
			case keyHome:
				cursor = 0
			case keyEnd:
				cursor = len(opts) - 1
			case keyEnter:
				f.finish(p.answer("", title, opts[cursor].Label))
				return nil
			case keyCancel:
				f.finish(p.answer("", title, p.c.Yellow("cancelado")))
				return errAborted
			default:
				if n := int(r - '0'); n >= 1 && n <= len(opts) {
					cursor = n - 1
				}
			}
		}
	})
	return cursor, err, ok
}

func (p *Prompter) menuMulti(title string, opts []Option, defs []bool) ([]int, error, bool) {
	checked := append([]bool(nil), defs...)
	cursor := 0
	var picked []int
	err, ok := p.interactive(func(f *frame) error {
		warn := ""
		for {
			lines := []string{p.answer("", title, "")}
			lines = append(lines, p.optionLines(f, opts, cursor, checked)...)
			if warn != "" {
				lines = append(lines, "  "+p.c.Yellow(f.fit(2, warn)))
			} else {
				lines = append(lines, p.help("  ", f.fit(2, "↑/↓ mover · Espaço marcar · a todos · Enter confirmar · Ctrl+C cancelar")))
			}
			warn = ""
			f.draw(lines)
			k, r, err := p.readKey()
			if err != nil {
				f.finish(p.answer("", title, ""))
				return err
			}
			switch k {
			case keyUp, keyLeft:
				cursor = (cursor - 1 + len(opts)) % len(opts)
			case keyDown, keyRight, keyTab:
				cursor = (cursor + 1) % len(opts)
			case keyHome:
				cursor = 0
			case keyEnd:
				cursor = len(opts) - 1
			case keySpace:
				checked[cursor] = !checked[cursor]
			case keyEnter:
				var labels []string
				for i, c := range checked {
					if c {
						picked = append(picked, i)
						labels = append(labels, opts[i].Label)
					}
				}
				if len(picked) == 0 {
					warn = "marque ao menos uma opção com Espaço"
					continue
				}
				f.finish(p.answer("", title, strings.Join(labels, ", ")))
				return nil
			case keyCancel:
				f.finish(p.answer("", title, p.c.Yellow("cancelado")))
				return errAborted
			default:
				switch {
				case r == 'a' || r == 'A':
					all := true
					for _, c := range checked {
						all = all && c
					}
					for i := range checked {
						checked[i] = !all
					}
				case r >= '1' && int(r-'0') <= len(opts):
					cursor = int(r - '1')
					checked[cursor] = !checked[cursor]
				}
			}
		}
	})
	return picked, err, ok
}

func (p *Prompter) menuConfirm(label string, def bool) (bool, error, bool) {
	// keep the label's leading blank lines and indentation
	text := strings.TrimLeft(label, " \n")
	lead := label[:len(label)-len(text)]
	indent := lead[strings.LastIndex(lead, "\n")+1:]
	fmt.Fprint(p.out, lead[:len(lead)-len(indent)])

	yes := def
	err, ok := p.interactive(func(f *frame) error {
		for {
			sim, nao := p.c.Dim("○ Sim"), p.c.BoldCyan("● Não")
			if yes {
				sim, nao = p.c.BoldCyan("● Sim"), p.c.Dim("○ Não")
			}
			f.draw([]string{
				p.answer(indent, text, "") + " " + sim + "  " + nao,
				p.help(indent+"  ", f.fit(len(indent)+2, "←/→ alternar · s/n responder · Enter confirmar · Ctrl+C cancelar")),
			})
			k, r, err := p.readKey()
			if err != nil {
				f.finish(p.answer(indent, text, ""))
				return err
			}
			switch {
			case k == keyLeft || k == keyRight || k == keyUp || k == keyDown || k == keyTab || k == keySpace:
				yes = !yes
			case k == keyEnter:
			case k == keyCancel:
				f.finish(p.answer(indent, text, p.c.Yellow("cancelado")))
				return errAborted
			case r == 's' || r == 'S' || r == 'y' || r == 'Y':
				yes = true
			case r == 'n' || r == 'N':
				yes = false
			default:
				continue
			}
			if k == keyEnter || r != 0 && k == keyOther {
				answer := "Não"
				if yes {
					answer = "Sim"
				}
				f.finish(p.answer(indent, text, answer))
				return nil
			}
		}
	})
	return yes, err, ok
}
