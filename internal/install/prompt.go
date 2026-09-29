package install

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Prompter asks questions on a terminal. With Yes set it never reads input
// and answers every question with its default.
type Prompter struct {
	in  *bufio.Reader
	out io.Writer
	Yes bool
}

func NewPrompter(in io.Reader, out io.Writer, yes bool) *Prompter {
	return &Prompter{in: bufio.NewReader(in), out: out, Yes: yes}
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
		mark := " "
		if marked(i) {
			mark = "*"
		}
		line := fmt.Sprintf("  %s %d) %s", mark, i+1, o.Label)
		if o.Hint != "" {
			line += "  — " + o.Hint
		}
		fmt.Fprintln(p.out, line)
	}
}

// Select asks for one option and returns its index.
func (p *Prompter) Select(title string, opts []Option, def int) (int, error) {
	if p.Yes {
		fmt.Fprintf(p.out, "%s: %s\n", title, opts[def].Label)
		return def, nil
	}
	fmt.Fprintln(p.out, title)
	p.printOptions(opts, func(i int) bool { return i == def })
	for {
		fmt.Fprintf(p.out, "Escolha [%d]: ", def+1)
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
		fmt.Fprintf(p.out, "  digite um número de 1 a %d\n", len(opts))
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
		fmt.Fprintf(p.out, "%s: %s\n", title, strings.Join(defLabels, ", "))
		return def, nil
	}
	fmt.Fprintln(p.out, title)
	p.printOptions(opts, func(i int) bool { return defs[i] })
	for {
		fmt.Fprintf(p.out, "Escolha um ou mais, separados por vírgula [%s]: ", strings.Join(defNums, ","))
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
		fmt.Fprintf(p.out, "  digite números de 1 a %d, ex.: 1,2\n", len(opts))
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
			fmt.Fprintf(p.out, "%s: %s\n", label, def)
		}
		return def, nil
	}
	for {
		if def != "" {
			fmt.Fprintf(p.out, "%s [%s]: ", label, def)
		} else {
			fmt.Fprintf(p.out, "%s: ", label)
		}
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
				fmt.Fprintln(p.out, "  ✗", err)
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
	hint := "s/N"
	if def {
		hint = "S/n"
	}
	for {
		fmt.Fprintf(p.out, "%s [%s]: ", label, hint)
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
		fmt.Fprintln(p.out, "  responda s ou n")
	}
}

// Wait blocks until Enter (no-op with Yes).
func (p *Prompter) Wait(msg string) error {
	if p.Yes {
		return nil
	}
	fmt.Fprint(p.out, msg)
	_, err := p.readLine()
	return err
}

func required(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("obrigatório")
	}
	return nil
}
