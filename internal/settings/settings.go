// Package settings has the helpers every provider uses to read and validate
// its configuration. Secrets come only from the process environment or from
// files referenced by it — never from tool arguments — so they never pass
// through the model's context.
package settings

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Warnf reports a non-fatal configuration problem (stderr by default).
var Warnf = func(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[devpulse] aviso: "+format+"\n", args...)
}

// ValidateHTTPSURL requires an absolute https URL without credentials,
// query or fragment, and returns it without a trailing slash.
func ValidateHTTPSURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("URL inválida: %w", err)
	}
	if u.Scheme != "https" {
		return "", errors.New("precisa começar com https://")
	}
	if u.Host == "" {
		return "", errors.New("URL sem host")
	}
	if u.User != nil {
		return "", errors.New("não coloque credenciais na URL")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("a URL não pode ter query string ou fragmento")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// CheckHTTPSURL is ValidateHTTPSURL for callers that only need the error.
func CheckHTTPSURL(raw string) error {
	_, err := ValidateHTTPSURL(raw)
	return err
}

// SecretFromEnv reads NAME, or the file pointed to by NAME_FILE.
// Setting both is rejected to avoid ambiguity.
func SecretFromEnv(getenv func(string) string, name string) (string, error) {
	direct := strings.TrimSpace(getenv(name))
	path := strings.TrimSpace(getenv(name + "_FILE"))
	if direct != "" && path != "" {
		return "", fmt.Errorf("defina %s ou %s_FILE, não os dois", name, name)
	}
	if path == "" {
		return direct, nil
	}
	s, err := ReadSecretFile(path)
	if err != nil {
		return "", fmt.Errorf("%s_FILE: %w", name, err)
	}
	return s, nil
}

// ReadSecretFile reads a file that must contain only a token.
func ReadSecretFile(path string) (string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !fi.Mode().IsRegular() {
		return "", errors.New("não é um arquivo regular")
	}
	if fi.Size() > 16*1024 {
		return "", errors.New("arquivo grande demais para conter só um token")
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
		Warnf("%s pode ser lido por outros usuários (permissões %o); considere chmod 600", path, fi.Mode().Perm())
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return "", errors.New("arquivo vazio")
	}
	return s, nil
}

// EnvBool is true only for values strconv.ParseBool accepts as true.
func EnvBool(v string) bool {
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	return err == nil && b
}

// CheckBool accepts "" or anything strconv.ParseBool understands.
func CheckBool(v string) error {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	if _, err := strconv.ParseBool(strings.TrimSpace(v)); err != nil {
		return fmt.Errorf("use true ou false (recebido %q)", v)
	}
	return nil
}

// Timeout global settings.
const (
	HTTPTimeoutEnv     = "DEVPULSE_HTTP_TIMEOUT"
	DefaultHTTPTimeout = 30 * time.Second
)

// LegacyHTTPTimeoutEnvs are the names used by pm-mcp and 7pace-mcp, still
// accepted (in this order of preference) when HTTPTimeoutEnv is not set.
var LegacyHTTPTimeoutEnvs = []string{"PM_MCP_HTTP_TIMEOUT", "SEVENPACE_HTTP_TIMEOUT"}

// ParseTimeout validates a per-request timeout (e.g. "30s", max 5m).
func ParseTimeout(v string) (time.Duration, error) {
	d, err := time.ParseDuration(strings.TrimSpace(v))
	if err != nil || d <= 0 || d > 5*time.Minute {
		return 0, fmt.Errorf("tempo limite inválido: %q (ex.: 30s, máx. 5m)", v)
	}
	return d, nil
}

// HTTPTimeout reads DEVPULSE_HTTP_TIMEOUT (or one of the legacy names).
func HTTPTimeout(getenv func(string) string) (time.Duration, error) {
	for _, name := range append([]string{HTTPTimeoutEnv}, LegacyHTTPTimeoutEnvs...) {
		if v := strings.TrimSpace(getenv(name)); v != "" {
			d, err := ParseTimeout(v)
			if err != nil {
				return 0, fmt.Errorf("%s: %w", name, err)
			}
			return d, nil
		}
	}
	return DefaultHTTPTimeout, nil
}

var orgNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,62}$`)

// CheckOrgName validates an organization slug that becomes part of a host name.
func CheckOrgName(v string) error {
	if !orgNameRe.MatchString(strings.TrimSpace(v)) {
		return fmt.Errorf("organização inválida: %q (use apenas letras, números e hífen)", v)
	}
	return nil
}
