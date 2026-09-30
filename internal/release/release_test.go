package release_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sidyjw/devpulse/internal/release"
	"github.com/sidyjw/devpulse/internal/release/releasetest"
)

func TestLatestAndExecutable(t *testing.T) {
	f := &releasetest.Fake{Name: "devpulse", Version: "0.2.0", Notes: "### Adicionado\n- update", Exe: []byte("novo binário")}
	c := f.Start(t)
	ctx := context.Background()

	rel, err := c.Latest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rel.Tag != "v0.2.0" || rel.Version != "0.2.0" || !strings.Contains(rel.Notes, "update") {
		t.Errorf("rel = %+v", rel)
	}
	for _, p := range releasetest.Platforms {
		goos, goarch, _ := strings.Cut(p, "/")
		exe, err := c.Executable(ctx, rel, "devpulse", goos, goarch)
		if err != nil || string(exe) != "novo binário" {
			t.Errorf("%s: exe = %q, err = %v", p, exe, err)
		}
	}
	if _, err := c.Executable(ctx, rel, "devpulse", "plan9", "amd64"); err == nil || !strings.Contains(err.Error(), "não tem") {
		t.Errorf("plataforma sem binário: %v", err)
	}

	if rel, err := c.Get(ctx, "v0.2.0"); err != nil || rel.Version != "0.2.0" {
		t.Errorf("Get = %+v, %v", rel, err)
	}
	if _, err := c.Get(ctx, "9.9.9"); err == nil || !strings.Contains(err.Error(), "não encontrado") {
		t.Errorf("versão inexistente: %v", err)
	}
}

func TestExecutableRejectsBadChecksum(t *testing.T) {
	f := &releasetest.Fake{Name: "devpulse", Version: "0.2.0", Exe: []byte("x"), BadSum: true}
	c := f.Start(t)
	rel, err := c.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Executable(context.Background(), rel, "devpulse", "linux", "amd64"); err == nil || !strings.Contains(err.Error(), "SHA256") {
		t.Errorf("hash errado deveria ser recusado: %v", err)
	}
}

func TestRejectsOtherHostsAndHTTP(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://evil.example/x", http.StatusFound)
	}))
	defer srv.Close()
	c := &release.Client{HTTP: srv.Client(), API: srv.URL, Hosts: []string{"127.0.0.1"}}
	if _, err := c.Latest(context.Background()); err == nil || !strings.Contains(err.Error(), "evil.example") {
		t.Errorf("redirecionamento para outro host deveria ser bloqueado: %v", err)
	}

	c = &release.Client{HTTP: http.DefaultClient, API: "http://127.0.0.1:1", Hosts: []string{"127.0.0.1"}}
	if _, err := c.Latest(context.Background()); err == nil || !strings.Contains(err.Error(), "https") {
		t.Errorf("http deveria ser recusado: %v", err)
	}
	if got := release.New().Hosts; len(got) == 0 || got[0] != "api.github.com" {
		t.Errorf("hosts padrão = %v", got)
	}
}

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.2.0", "0.1.9", 1},
		{"v1.0.0", "1.0.0", 0},
		{"1.0.0-rc.1", "1.0.0", -1},
		{"1.0.0-rc.2", "1.0.0-rc.10", -1},
		{"1.0.0-alpha", "1.0.0-1", 1},
		{"1.0.0-rc.1", "1.0.0-rc.1.1", -1},
		{"0.10.0", "0.9.0", 1},
		{"1.2.3+build", "1.2.3", 0},
	}
	for _, c := range cases {
		got, err := release.Compare(c.a, c.b)
		if err != nil || got != c.want {
			t.Errorf("Compare(%s, %s) = %d, %v; want %d", c.a, c.b, got, err, c.want)
		}
	}
	for _, bad := range []string{"dev", "1.2", "a.b.c", ""} {
		if _, err := release.Compare(bad, "1.0.0"); err == nil {
			t.Errorf("%q deveria ser inválida", bad)
		}
	}
}
