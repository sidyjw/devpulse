package install

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/sidyjw/devpulse/internal/ui"
)

// Prompter asks questions on a terminal. With Yes set it never reads input
// and answers every question with its default.
type Prompter struct {
	in  *bufio.Reader
	out io.Writer
	c   ui.Palette
	Yes bool
	// keys is set when stdin and stdout are a terminal: menus and yes/no
	// questions are then answered with the arrow keys (see menu.go).
	keys *keyboard
}

func NewPrompter(in io.Reader, out io.Writer, yes bool) *Prompter {
	p := &Prompter{in: bufio.NewReader(in), out: out, c: ui.For(out), Yes: yes}
	fi, okIn := in.(*os.File)
	fo, okOut := out.(*os.File)
	if !yes && okIn && okOut && ui.IsTerminal(fi) && ui.EnableVT(fo) {
		p.keys = &keyboard{
			raw:   func() (func(), error) { return ui.MakeRaw(fi) },
			width: func() int { return ui.Width(fo) },
		}
	}
	return p
}

var errNoInput = errors.New("a entrada terminou; para rodar sem perguntas use --yes com as flags (veja -h)")

func (p *Prompter) readLine() (string, error) {
	s, err := p.in.ReadString('\n')
	if err != nil && (s == "" || !errors.Is(err, io.EOF)) {
		if errors.Is(err, io.EOF) {
			return "", errNoInput
		}
		return "", err
	}
	return strings.TrimSpace(s), nil
}

// Option is one choice of a menu.
type Option struct {
	Label string
	Hint  string
}

func (p *Prompter) printOptions(opts []Option, marked func(int) bool) {
	for i, o := range opts {
		mark, label := " ", o.Label
		switch {
		case marked(i) && p.c.Enabled():
			mark, label = p.c.Green("●"), p.c.Bold(label)
		case marked(i):
			mark = "*"
		case p.c.Enabled():
			mark = p.c.Dim("○")
		}
		line := fmt.Sprintf("  %s %s %s", mark, p.c.Cyan(fmt.Sprintf("%d)", i+1)), label)
		if o.Hint != "" {
			line += p.c.Dim("  — " + o.Hint)
		}
		fmt.Fprintln(p.out, line)
	}
}

// question prints the title of a menu.
func (p *Prompter) question(title string) {
	if p.c.Enabled() {
		title = p.c.BoldCyan("?") + " " + p.c.Bold(title)
	}
	fmt.Fprintln(p.out, title)
}

// ask prints "label [def]: " and waits on the same line.
func (p *Prompter) ask(label, def string) {
	if def != "" {
		def = " " + p.c.Dim("["+def+"]")
	}
	if p.c.Enabled() {
		text := strings.TrimLeft(label, " \n")
		label = label[:len(label)-len(text)] + p.c.BoldCyan("?") + " " + p.c.Bold(text)
	}
	fmt.Fprintf(p.out, "%s%s: ", label, def)
}

// choose prints the prompt under a menu.
func (p *Prompter) choose(label, def string) {
	if p.c.Enabled() {
		label = p.c.Cyan("›") + " " + label
	}
	fmt.Fprintf(p.out, "%s %s: ", label, p.c.Dim("["+def+"]"))
}

// answered echoes a default taken without asking (--yes).
func (p *Prompter) answered(label, value string) {
	fmt.Fprintf(p.out, "%s: %s\n", label, p.c.Cyan(value))
}

func (p *Prompter) retry(format string, args ...any) {
	fmt.Fprintln(p.out, p.c.Yellow("  "+fmt.Sprintf(format, args...)))
}

// Select asks for one option and returns its index.
func (p *Prompter) Select(title string, opts []Option, def int) (int, error) {
	if p.Yes {
		p.answered(title, opts[def].Label)
		return def, nil
	}
	if p.keys != nil {
		if i, err, ok := p.menuSelect(title, opts, def); ok {
			return i, err
		}
	}
	p.question(title)
	p.printOptions(opts, func(i int) bool { return i == def })
	for {
		p.choose("Escolha", strconv.Itoa(def+1))
		s, err := p.readLine()
		if err != nil {
			return 0, err
		}
		if s == "" {
			return def, nil
		}
		if n, err := strconv.Atoi(s); err == nil && n >= 1 && n <= len(opts) {
			return n - 1, nil
		}
		p.retry("digite um número de 1 a %d", len(opts))
	}
}

// MultiSelect asks for one or more options ("1,3"); defs are preselected.
func (p *Prompter) MultiSelect(title string, opts []Option, defs []bool) ([]int, error) {
	var def []int
	var defLabels, defNums []string
	for i, d := range defs {
		if d {
			def = append(def, i)
			defLabels = append(defLabels, opts[i].Label)
			defNums = append(defNums, strconv.Itoa(i+1))
		}
	}
	if p.Yes {
		if len(def) == 0 {
			return nil, fmt.Errorf("%s: nenhuma opção padrão; informe pela flag", title)
		}
		p.answered(title, strings.Join(defLabels, ", "))
		return def, nil
	}
	if p.keys != nil {
		if idx, err, ok := p.menuMulti(title, opts, defs); ok {
			return idx, err
		}
	}
	p.question(title)
	p.printOptions(opts, func(i int) bool { return defs[i] })
	for {
		p.choose("Escolha um ou mais, separados por vírgula", strings.Join(defNums, ","))
		s, err := p.readLine()
		if err != nil {
			return nil, err
		}
		if s == "" && len(def) > 0 {
			return def, nil
		}
		var out []int
		seen := map[int]bool{}
		ok := s != ""
		for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
			n, err := strconv.Atoi(part)
			if err != nil || n < 1 || n > len(opts) {
				ok = false
				break
			}
			if !seen[n-1] {
				seen[n-1] = true
				out = append(out, n-1)
			}
		}
		if ok && len(out) > 0 {
			return out, nil
		}
		p.retry("digite números de 1 a %d, ex.: 1,2", len(opts))
	}
}

// Input asks for a value; Enter keeps def. validate may be nil.
func (p *Prompter) Input(label, def string, validate func(string) error) (string, error) {
	if p.Yes {
		if validate != nil {
			if err := validate(def); err != nil {
				return "", fmt.Errorf("%s: %w", label, err)
			}
		}
		if def != "" {
			p.answered(label, def)
		}
		return def, nil
	}
	for {
		p.ask(label, def)
		s, err := p.readLine()
		if err != nil {
			return "", err
		}
		if s == "" {
			s = def
		} else if s == "-" {
			s = "" // explicit "clear the default"
		}
		if validate != nil {
			if err := validate(s); err != nil {
				fmt.Fprintln(p.out, p.c.Mark("  ✗ "+err.Error()))
				continue
			}
		}
		return s, nil
	}
}

// Confirm asks a yes/no question.
func (p *Prompter) Confirm(label string, def bool) (bool, error) {
	if p.Yes {
		return def, nil
	}
	if p.keys != nil {
		if yes, err, ok := p.menuConfirm(label, def); ok {
			return yes, err
		}
	}
	hint := "s/N"
	if def {
		hint = "S/n"
	}
	for {
		p.ask(label, hint)
		s, err := p.readLine()
		if err != nil {
			return false, err
		}
		switch strings.ToLower(s) {
		case "":
			return def, nil
		case "s", "sim", "y", "yes":
			return true, nil
		case "n", "nao", "não", "no":
			return false, nil
		}
		p.retry("responda s ou n")
	}
}

// Wait blocks until Enter (no-op with Yes).
func (p *Prompter) Wait(msg string) error {
	if p.Yes {
		return nil
	}
	fmt.Fprint(p.out, p.c.Cyan(msg))
	_, err := p.readLine()
	return err
}

func required(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("obrigatório")
	}
	return nil
}
