// Package release finds and downloads the published releases of the project
// on GitHub, for `devpulse update`. Every download is checked against the
// release's SHA256SUMS.txt, requests only go to GitHub's hosts and no
// credential is ever sent.
package release

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/sidyjw/devpulse/internal/httpx"
)

// Repo is the GitHub repository that publishes the releases.
const Repo = "sidyjw/devpulse"

// SumsFile is the checksum file attached to every release.
const SumsFile = "SHA256SUMS.txt"

const (
	maxJSON    = 1 << 20   // API responses and SHA256SUMS.txt
	maxArchive = 100 << 20 // release archives and the executable inside them
)

// DefaultHosts are the only hosts the client talks to: the API, the release
// download URL and the storage it redirects to.
var DefaultHosts = []string{
	"api.github.com",
	"github.com",
	"objects.githubusercontent.com",
	"release-assets.githubusercontent.com",
}

// Client talks to the GitHub releases API.
type Client struct {
	HTTP  *http.Client
	API   string   // e.g. https://api.github.com (no trailing slash)
	Hosts []string // allowed hosts, for every request and redirect
}

// New returns a client for the public GitHub API.
func New() *Client {
	return &Client{HTTP: &http.Client{Timeout: 5 * time.Minute}, API: "https://api.github.com", Hosts: DefaultHosts}
}

// Release is one published version.
type Release struct {
	Tag        string // e.g. v0.2.0
	Version    string // e.g. 0.2.0
	Notes      string
	Prerelease bool
	Assets     map[string]string // file name -> download URL
}

// Latest returns the newest release that is not a pre-release.
func (c *Client) Latest(ctx context.Context) (*Release, error) {
	return c.release(ctx, "/repos/"+Repo+"/releases/latest")
}

// Get returns the release of a version ("0.2.0" or "v0.2.0").
func (c *Client) Get(ctx context.Context, version string) (*Release, error) {
	return c.release(ctx, "/repos/"+Repo+"/releases/tags/v"+url.PathEscape(strings.TrimPrefix(version, "v")))
}

func (c *Client) release(ctx context.Context, p string) (*Release, error) {
	raw, err := c.get(ctx, c.API+p, maxJSON)
	if err != nil {
		return nil, err
	}
	var r struct {
		Tag        string `json:"tag_name"`
		Body       string `json:"body"`
		Prerelease bool   `json:"prerelease"`
		Assets     []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("resposta inesperada do GitHub: %v", err)
	}
	if r.Tag == "" {
		return nil, errors.New("resposta inesperada do GitHub: release sem tag")
	}
	rel := &Release{Tag: r.Tag, Version: strings.TrimPrefix(r.Tag, "v"), Notes: r.Body, Prerelease: r.Prerelease, Assets: map[string]string{}}
	for _, a := range r.Assets {
		rel.Assets[a.Name] = a.URL
	}
	return rel, nil
}

// ArchiveName is the release file for a platform, as built by release.yml.
func ArchiveName(name, version, goos, goarch string) string {
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("%s_%s_%s_%s%s", name, version, goos, goarch, ext)
}

// Executable downloads the archive of the platform, checks it against
// SHA256SUMS.txt and returns the executable inside it.
func (c *Client) Executable(ctx context.Context, rel *Release, name, goos, goarch string) ([]byte, error) {
	archive := ArchiveName(name, rel.Version, goos, goarch)
	archiveURL, ok := rel.Assets[archive]
	if !ok {
		return nil, fmt.Errorf("a release %s não tem %s (sistema sem binário publicado?)", rel.Tag, archive)
	}
	sumsURL, ok := rel.Assets[SumsFile]
	if !ok {
		return nil, fmt.Errorf("a release %s não tem %s; atualização recusada", rel.Tag, SumsFile)
	}
	sums, err := c.get(ctx, sumsURL, maxJSON)
	if err != nil {
		return nil, err
	}
	want, err := findSum(sums, archive)
	if err != nil {
		return nil, err
	}
	data, err := c.get(ctx, archiveURL, maxArchive)
	if err != nil {
		return nil, err
	}
	got := sha256.Sum256(data)
	if hex.EncodeToString(got[:]) != want {
		return nil, fmt.Errorf("o SHA256 de %s não confere com %s; atualização recusada", archive, SumsFile)
	}
	if goos == "windows" {
		return fromZip(data, name+".exe")
	}
	return fromTarGz(data, name)
}

// findSum returns the hex SHA256 of file in a sha256sum listing.
func findSum(sums []byte, file string) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == file {
			sum := strings.ToLower(f[0])
			if len(sum) != 64 {
				break
			}
			return sum, nil
		}
	}
	return "", fmt.Errorf("%s não lista %s; atualização recusada", SumsFile, file)
}

func fromZip(data []byte, exe string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("arquivo zip inválido: %v", err)
	}
	for _, f := range zr.File {
		if path.Base(f.Name) != exe || !f.Mode().IsRegular() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return readLimited(rc, exe)
	}
	return nil, fmt.Errorf("%s não encontrado no arquivo", exe)
}

func fromTarGz(data []byte, exe string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("arquivo tar.gz inválido: %v", err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%s não encontrado no arquivo", exe)
		}
		if err != nil {
			return nil, fmt.Errorf("arquivo tar.gz inválido: %v", err)
		}
		if h.Typeflag == tar.TypeReg && path.Base(h.Name) == exe {
			return readLimited(tr, exe)
		}
	}
}

func readLimited(r io.Reader, what string) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxArchive+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxArchive {
		return nil, fmt.Errorf("%s excede %d bytes", what, maxArchive)
	}
	return b, nil
}

func (c *Client) allowed(u *url.URL) error {
	if u.Scheme != "https" {
		return fmt.Errorf("URL recusada (não é https): %s", u.Redacted())
	}
	for _, h := range c.Hosts {
		if strings.EqualFold(u.Hostname(), h) {
			return nil
		}
	}
	return fmt.Errorf("host não permitido: %s", u.Hostname())
}

// get fetches u, following redirects only to the allowed hosts.
func (c *Client) get(ctx context.Context, u string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if err := c.allowed(req.URL); err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", httpx.UserAgent)
	if strings.HasPrefix(u, c.API) {
		req.Header.Set("Accept", "application/vnd.github+json")
	}
	hc := *c.HTTP
	hc.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("redirecionamentos demais")
		}
		return c.allowed(r.URL)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("falha ao acessar o GitHub: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("não encontrado no GitHub: %s", u)
	}
	if resp.StatusCode != http.StatusOK {
		msg := fmt.Sprintf("o GitHub respondeu HTTP %d em %s", resp.StatusCode, u)
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
			msg += " (limite de requisições da API; tente de novo mais tarde)"
		}
		return nil, errors.New(msg)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("falha ao baixar %s: %w", u, err)
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s excede %d bytes", u, limit)
	}
	return b, nil
}

// Compare compares two versions MAJOR.MINOR.PATCH[-pre] (a leading "v" is
// ignored) and returns -1, 0 or +1. A pre-release sorts before its release.
func Compare(a, b string) (int, error) {
	va, err := parse(a)
	if err != nil {
		return 0, err
	}
	vb, err := parse(b)
	if err != nil {
		return 0, err
	}
	for i := 0; i < 3; i++ {
		if va.n[i] != vb.n[i] {
			return sign(va.n[i] - vb.n[i]), nil
		}
	}
	switch {
	case va.pre == vb.pre:
		return 0, nil
	case va.pre == "":
		return 1, nil
	case vb.pre == "":
		return -1, nil
	}
	return comparePre(va.pre, vb.pre), nil
}

type version struct {
	n   [3]int
	pre string
}

func parse(s string) (version, error) {
	var v version
	core, pre, _ := strings.Cut(strings.TrimPrefix(strings.TrimSpace(s), "v"), "-")
	core, _, _ = strings.Cut(core, "+") // build metadata does not count
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return v, fmt.Errorf("versão inválida %q", s)
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, fmt.Errorf("versão inválida %q", s)
		}
		v.n[i] = n
	}
	v.pre, _, _ = strings.Cut(pre, "+")
	return v, nil
}

// comparePre follows semver: dot-separated identifiers, numeric ones compared
// as numbers and sorting before alphanumeric ones.
func comparePre(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		na, ea := strconv.Atoi(pa[i])
		nb, eb := strconv.Atoi(pb[i])
		switch {
		case ea == nil && eb == nil:
			if na != nb {
				return sign(na - nb)
			}
		case ea == nil:
			return -1
		case eb == nil:
			return 1
		default:
			if c := strings.Compare(pa[i], pb[i]); c != 0 {
				return c
			}
		}
	}
	return sign(len(pa) - len(pb))
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}
