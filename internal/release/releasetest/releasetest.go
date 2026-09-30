// Package releasetest serves a fake GitHub release API over TLS for tests.
package releasetest

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/sidyjw/devpulse/internal/release"
)

// Platforms are the targets built by release.yml.
var Platforms = []string{"windows/amd64", "windows/arm64", "linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64"}

// Fake is one published release.
type Fake struct {
	Name, Version, Notes string
	Exe                  []byte // content of the executable in every archive
	BadSum               bool   // SHA256SUMS.txt lists a wrong hash

	mu       sync.Mutex
	requests []string
}

// Start serves the release and returns a client pointed at it.
func (f *Fake) Start(t *testing.T) *release.Client {
	t.Helper()
	files := map[string][]byte{}
	var sums strings.Builder
	for _, p := range Platforms {
		goos, goarch, _ := strings.Cut(p, "/")
		name := release.ArchiveName(f.Name, f.Version, goos, goarch)
		dir := strings.TrimSuffix(strings.TrimSuffix(name, ".zip"), ".tar.gz")
		var data []byte
		if goos == "windows" {
			data = Zip(t, dir+"/"+f.Name+".exe", f.Exe)
		} else {
			data = TarGz(t, dir+"/"+f.Name, f.Exe)
		}
		files[name] = data
		sum := sha256.Sum256(data)
		if f.BadSum {
			sum = sha256.Sum256([]byte("outro"))
		}
		fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(sum[:]), name)
	}
	files[release.SumsFile] = []byte(sums.String())

	var srv *httptest.Server
	mux := http.NewServeMux()
	releaseJSON := func(w http.ResponseWriter) {
		type asset struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		}
		var assets []asset
		for n := range files {
			assets = append(assets, asset{n, srv.URL + "/download/v" + f.Version + "/" + n})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v" + f.Version, "body": f.Notes, "assets": assets})
	}
	mux.HandleFunc("/repos/"+release.Repo+"/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		f.log(r)
		releaseJSON(w)
	})
	mux.HandleFunc("/repos/"+release.Repo+"/releases/tags/", func(w http.ResponseWriter, r *http.Request) {
		f.log(r)
		if strings.TrimPrefix(r.URL.Path, "/repos/"+release.Repo+"/releases/tags/") != "v"+f.Version {
			http.NotFound(w, r)
			return
		}
		releaseJSON(w)
	})
	mux.HandleFunc("/download/", func(w http.ResponseWriter, r *http.Request) {
		f.log(r)
		n := r.URL.Path[strings.LastIndexByte(r.URL.Path, '/')+1:]
		data, ok := files[n]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	})
	srv = httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	return &release.Client{HTTP: srv.Client(), API: srv.URL, Hosts: []string{"127.0.0.1"}}
}

// Zip builds a zip archive with one file.
func Zip(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	w, err := zw.Create(name)
	if err == nil {
		_, err = w.Write(content)
	}
	if err == nil {
		err = zw.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// TarGz builds a tar.gz archive with one file.
func TarGz(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg})
	if err == nil {
		_, err = tw.Write(content)
	}
	if err == nil {
		err = tw.Close()
	}
	if err == nil {
		err = gz.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func (f *Fake) log(r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, r.URL.Path)
	f.mu.Unlock()
}

// Requests returns the paths requested so far.
func (f *Fake) Requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}
