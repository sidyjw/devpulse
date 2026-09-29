// Package httpx is the small JSON-over-HTTPS client shared by every provider.
// It never follows redirects, caps response sizes and scrubs credentials from
// every error message.
package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// MaxResponseBytes limits how much of a response body is read.
const MaxResponseBytes = 8 << 20 // 8 MiB is plenty for any page of results

// UserAgent is sent with every request; main sets it to include the version.
var UserAgent = "devpulse"

// NewHTTPClient returns a client that never follows redirects, so a
// credential can only ever be sent to the host that was configured.
func NewHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// Client is a JSON-over-HTTPS helper bound to one API root and credential.
type Client struct {
	HTTP    *http.Client
	Root    string            // absolute base URL, no trailing slash
	Auth    string            // value of the Authorization header
	Secrets []string          // values scrubbed from every error message
	Name    string            // e.g. "7pace" or "Azure DevOps", used in errors
	Extra   map[string]string // extra headers
}

// Error is a non-2xx response.
type Error struct {
	Service string
	Method  string
	Path    string
	Status  int
	Body    string
}

func (e *Error) Error() string {
	msg := fmt.Sprintf("%s respondeu HTTP %d em %s %s", e.Service, e.Status, e.Method, e.Path)
	switch e.Status {
	case http.StatusUnauthorized:
		msg += " (token inválido ou expirado)"
	case http.StatusForbidden:
		msg += " (token sem permissão para esta operação)"
	case http.StatusNotFound:
		msg += " (recurso não encontrado)"
	}
	if e.Status >= 300 && e.Status < 400 {
		msg += " (redirecionamento bloqueado por segurança — confira a URL base)"
	}
	if e.Body != "" {
		msg += ": " + e.Body
	}
	return msg
}

// Do sends a request with an optional JSON body and decodes a JSON
// response into out (if out is non-nil and the body is non-empty).
// pathAndQuery must start with "/".
func (c *Client) Do(ctx context.Context, method, pathAndQuery string, body any, out any) error {
	return c.DoCT(ctx, method, pathAndQuery, "application/json", body, out)
}

// DoCT is Do with an explicit request Content-Type.
func (c *Client) DoCT(ctx context.Context, method, pathAndQuery, contentType string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Root+pathAndQuery, rdr)
	if err != nil {
		return c.scrub(err)
	}
	req.Header.Set("Authorization", c.Auth)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", UserAgent)
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range c.Extra {
		req.Header.Set(k, v)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "Client.Timeout") {
			return fmt.Errorf("%s não respondeu a tempo (%s %s)", c.Name, method, StripQuery(pathAndQuery))
		}
		return c.scrub(fmt.Errorf("falha ao chamar %s: %w", c.Name, err))
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil {
		return c.scrub(fmt.Errorf("falha ao ler resposta de %s: %w", c.Name, err))
	}
	if len(raw) > MaxResponseBytes {
		return fmt.Errorf("resposta de %s excedeu %d bytes", c.Name, MaxResponseBytes)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &Error{
			Service: c.Name,
			Method:  method,
			Path:    StripQuery(pathAndQuery),
			Status:  resp.StatusCode,
			Body:    c.scrubString(Truncate(strings.TrimSpace(string(raw)), 600)),
		}
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("resposta inesperada de %s em %s: %v", c.Name, StripQuery(pathAndQuery), err)
	}
	return nil
}

func (c *Client) scrub(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(c.scrubString(err.Error()))
}

func (c *Client) scrubString(s string) string {
	for _, sec := range c.Secrets {
		if len(sec) >= 4 {
			s = strings.ReplaceAll(s, sec, "[REDACTED]")
		}
	}
	return s
}

// StripQuery drops the query string from a path.
func StripQuery(p string) string {
	if i := strings.IndexByte(p, '?'); i >= 0 {
		return p[:i]
	}
	return p
}

// Truncate shortens s to n bytes, adding an ellipsis.
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
