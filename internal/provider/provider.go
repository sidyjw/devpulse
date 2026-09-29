// Package provider defines how a project-management tool (a Provider) and
// its modules (Components) plug into the server and the installer.
//
// A Provider is a tool such as Azure DevOps or Jira. Each Component is one
// module of it (Boards, 7pace Timetracker, …) that the user can enable
// independently. Components describe their own settings so the installer
// can ask for them and validate them with the same rules the server uses.
//
// Tool names are global: new providers must prefix theirs (e.g. "jira_…");
// registering a duplicate name panics at startup.
package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/sidiney/pm-mcp/internal/mcp"
)

// SettingKind tells the installer how to ask for and store a value.
type SettingKind int

const (
	String     SettingKind = iota
	URL                    // https URL
	SecretFile             // path to a file holding a token (NAME_FILE)
	Bool                   // "true" / "false"; written only when true
	Duration               // e.g. "30s"
)

// Setting is one environment variable a component reads.
type Setting struct {
	Env      string // e.g. "SEVENPACE_ORGANIZATION"
	Label    string // question shown by the installer
	Help     string // one-line hint
	Kind     SettingKind
	Required bool
	Advanced bool // only asked when the user opts into advanced options
	// Default returns the suggested value (home is the user's home directory).
	Default func(home string) string
	// Validate checks a non-empty value with the same rules the server uses.
	Validate func(string) error
	// SecretAlt is the env var that holds the secret inline, for SecretFile
	// settings (e.g. "SEVENPACE_TOKEN" for "SEVENPACE_TOKEN_FILE").
	SecretAlt string
	// Guide explains where to get the value (optional). For SecretFile
	// settings it is shown only when the file still has to be created.
	Guide *Guide
}

// Guide is a step-by-step explanation shown by the installer. Steps may
// reference other settings as {ENV} or {ENV|fallback}; the installer fills
// in what the user has already answered.
type Guide struct {
	Title string
	Steps []string
}

// Instance is a component built from a configuration.
type Instance interface {
	// Register adds the component's tools to the server.
	Register(s *mcp.Server)
	// Check tests the connection and prints a report to w (used by -check).
	Check(ctx context.Context, w io.Writer) error
}

// Component is one module of a provider.
type Component struct {
	ID          string // unique within the provider, e.g. "boards"
	Name        string // e.g. "Boards"
	Description string // one line shown by the installer
	Settings    []Setting
	// Instructions are appended to the server instructions when enabled.
	Instructions string
	// Enabled reports whether the environment configures this component.
	Enabled func(getenv func(string) string) bool
	// Build creates the component; it may look up components built before it.
	Build func(bc *BuildContext) (Instance, error)
}

// Provider is a project-management tool.
type Provider struct {
	ID         string // e.g. "azuredevops"
	Name       string // e.g. "Azure DevOps"
	Components []Component
}

// Key returns the qualified id of a component ("azuredevops.boards").
func Key(p Provider, c Component) string { return p.ID + "." + c.ID }

// BuildContext is what a component gets to build itself.
type BuildContext struct {
	Getenv    func(string) string
	HTTP      *http.Client
	instances map[string]Instance
}

// Get returns an instance built earlier (by qualified key), or nil.
func (bc *BuildContext) Get(key string) Instance { return bc.instances[key] }

// Active is a built component.
type Active struct {
	Key, Name string
	Instance  Instance
}

// GlobalSettings are read by the server itself, not by a component.
var GlobalSettings = []Setting{{
	Env:      "PM_MCP_HTTP_TIMEOUT",
	Label:    "Tempo limite por requisição HTTP",
	Help:     "ex.: 30s (máx. 5m)",
	Kind:     Duration,
	Advanced: true,
	Default:  func(string) string { return "30s" },
}}

// Activate builds every component the environment enables, in declaration
// order, and returns them with the combined server instructions.
func Activate(ps []Provider, getenv func(string) string, hc *http.Client, baseInstructions string) ([]Active, string, error) {
	bc := &BuildContext{Getenv: getenv, HTTP: hc, instances: map[string]Instance{}}
	var out []Active
	var instr []string
	if baseInstructions != "" {
		instr = append(instr, baseInstructions)
	}
	for _, p := range ps {
		for _, c := range p.Components {
			if !c.Enabled(getenv) {
				continue
			}
			key := Key(p, c)
			inst, err := c.Build(bc)
			if err != nil {
				return nil, "", fmt.Errorf("%s / %s: %w", p.Name, c.Name, err)
			}
			bc.instances[key] = inst
			out = append(out, Active{Key: key, Name: p.Name + " / " + c.Name, Instance: inst})
			if c.Instructions != "" {
				instr = append(instr, c.Instructions)
			}
		}
	}
	if len(out) == 0 {
		return nil, "", errors.New("nada configurado: " + describeAll(ps))
	}
	return out, strings.Join(instr, "\n"), nil
}

func describeAll(ps []Provider) string {
	var parts []string
	for _, p := range ps {
		for _, c := range p.Components {
			var req []string
			for _, s := range c.Settings {
				if s.Required {
					req = append(req, s.Env)
				}
			}
			parts = append(parts, fmt.Sprintf("%s / %s (%s)", p.Name, c.Name, strings.Join(req, ", ")))
		}
	}
	return "defina as variáveis de ao menos um componente: " + strings.Join(parts, "; ") +
		". Dica: rode `pm-mcp install`"
}

// Find returns the provider and component with these ids.
func Find(ps []Provider, providerID, componentID string) (*Provider, *Component) {
	for i := range ps {
		if ps[i].ID != providerID {
			continue
		}
		for j := range ps[i].Components {
			if ps[i].Components[j].ID == componentID {
				return &ps[i], &ps[i].Components[j]
			}
		}
		return &ps[i], nil
	}
	return nil, nil
}
