// Package providers lists every project-management tool this build supports.
// To add one, implement provider.Provider in its own package and append it
// here.
package providers

import (
	"github.com/sidiney/devpulse/internal/provider"
	"github.com/sidiney/devpulse/internal/providers/azuredevops"
)

// All returns the providers in the order the installer shows them.
func All() []provider.Provider {
	return []provider.Provider{
		azuredevops.Provider(),
	}
}
