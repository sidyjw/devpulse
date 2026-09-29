package install

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

const (
	up    = "\x1b[A"
	down  = "\x1b[B"
	right = "\x1b[C"
)

// keyPrompter is a Prompter driven by the arrow keys, reading keys from in.
func keyPrompter(in string, raws *int) (*Prompter, *bytes.Buffer) {
	var out bytes.Buffer
	p := NewPrompter(strings.NewReader(in), &out, false)
	p.keys = &keyboard{
		raw: func() (func(), error) {
			*raws++
			return func() { *raws-- }, nil
		},
		width: func() int { return 40 },
	}
	return p, &out
}

var abc = []Option{{Label: "a", Hint: "uma dica bem comprida que não cabe na largura"}, {Label: "b"}, {Label: "c"}}

func TestMenuSelect(t *testing.T) {
	var raws int
	p, out := keyPrompter(down+down+down+up+"\r"+"3\r", &raws)
	if i, err := p.Select("Qual?", abc, 0); err != nil || i != 2 {
		t.Fatalf("↓↓↓↑ a partir de a (com volta) deveria dar c: %d %v", i, err)
	}
	if i, err := p.Select("Qual?", abc, 0); err != nil || i != 2 {
		t.Fatalf("a tecla 3 deveria ir para c: %d %v", i, err)
	}
	if raws != 0 {
		t.Error("o terminal não voltou ao modo normal")
	}
	s := out.String()
	for _, want := range []string{"↑/↓ mover", "Qual? c\n", "…", showCursor} {
		if !strings.Contains(s, want) {
			t.Errorf("saída sem %q:\n%q", want, s)
		}
	}
	for _, l := range strings.Split(s, "\n") {
		if n := len([]rune(stripCSI(l))); n > 40 {
			t.Errorf("linha maior que o terminal (%d): %q", n, l)
		}
	}
}

func TestMenuMulti(t *testing.T) {
	var raws int
	p, _ := keyPrompter(" "+down+down+" \r", &raws)
	idx, err := p.MultiSelect("Quais?", abc, []bool{true, true, false})
	if err != nil || len(idx) != 2 || idx[0] != 1 || idx[1] != 2 {
		t.Fatalf("idx = %v err = %v", idx, err)
	}

	// nothing checked: Enter warns and waits; "a" checks everything
	p, out := keyPrompter("\ra\r", &raws)
	idx, err = p.MultiSelect("Quais?", abc, []bool{false, false, false})
	if err != nil || len(idx) != 3 || !strings.Contains(out.String(), "marque ao menos uma") {
		t.Fatalf("idx = %v err = %v\n%s", idx, err, out)
	}
}

func TestMenuConfirmAndCancel(t *testing.T) {
	var raws int
	p, out := keyPrompter(right+"\r"+"n"+"\x03", &raws)
	if yes, err := p.Confirm("\n  Aplicar?", true); err != nil || yes {
		t.Fatalf("→ deveria trocar para Não: %v %v", yes, err)
	}
	if !strings.HasPrefix(stripCSI(out.String()), "\n") {
		t.Error("a linha em branco antes da pergunta sumiu")
	}
	if yes, err := p.Confirm("Criar?", true); err != nil || yes {
		t.Fatalf("n deveria responder Não: %v %v", yes, err)
	}
	if _, err := p.Select("Qual?", abc, 0); !errors.Is(err, errAborted) {
		t.Fatalf("Ctrl+C deveria cancelar: %v", err)
	}
	if raws != 0 {
		t.Error("o terminal não voltou ao modo normal")
	}
}

func TestMenuFallsBackWhenRawFails(t *testing.T) {
	var out bytes.Buffer
	p := NewPrompter(strings.NewReader("2\n"), &out, false)
	p.keys = &keyboard{raw: func() (func(), error) { return nil, errors.New("sem console") }, width: func() int { return 0 }}
	if i, err := p.Select("Qual?", abc, 0); err != nil || i != 1 {
		t.Fatalf("deveria aceitar o número digitado: %d %v", i, err)
	}
	if p.keys != nil {
		t.Error("depois da falha o Prompter deveria parar de tentar as setas")
	}
}

func stripCSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			for i += 2; i < len(s) && (s[i] < 0x40 || s[i] > 0x7e); i++ {
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func TestExpandGuide(t *testing.T) {
	lookup := func(k string) string { return map[string]string{"ORG_URL": "https://dev.azure.com/acme/"}[k] }
	for in, want := range map[string]string{
		"Abra {ORG_URL|https://x}/_usersSettings/tokens.": "Abra https://dev.azure.com/acme/_usersSettings/tokens.",
		"https://{ORG|<org>}.timehub.7pace.com":           "https://<org>.timehub.7pace.com",
		"sem chaves { soltas":                             "sem chaves { soltas",
	} {
		if got := expandGuide(in, lookup); got != want {
			t.Errorf("expandGuide(%q) = %q, esperava %q", in, got, want)
		}
	}
}
