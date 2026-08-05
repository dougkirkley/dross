package forge

import (
	"os"
	"strings"
)

// App is one host's OAuth application, already resolved: the secret has been
// read, not named.
type App struct {
	ClientID     string
	ClientSecret string
	BaseURL      string
}

// RegisterAll configures the hosts it has complete credentials for and returns
// their names.
//
// Silent about the rest, deliberately. A dross that nobody has registered an
// OAuth application for is the ordinary case for anyone running it locally, and
// the right behaviour there is to offer no hosts rather than to complain at
// every startup about an integration that was never asked for.
//
// [apps] is keyed by host id. Anything not named here still gets a look at the
// environment, so a deployment can configure GitHub entirely with
// GITHUB_CLIENT_ID and GITHUB_CLIENT_SECRET and never touch the config file.
func RegisterAll(apps map[string]App) []string {
	var on []string
	for _, name := range []string{"github", "gitlab"} {
		a := apps[name]
		if a.ClientID == "" {
			a.ClientID = os.Getenv(envName(name, "CLIENT_ID"))
		}
		if a.ClientSecret == "" {
			a.ClientSecret = os.Getenv(envName(name, "CLIENT_SECRET"))
		}
		if a.BaseURL == "" {
			a.BaseURL = os.Getenv(envName(name, "BASE_URL"))
		}
		cfg := Config{ClientID: a.ClientID, ClientSecret: a.ClientSecret, BaseURL: a.BaseURL}
		if !cfg.Configured() {
			continue
		}
		switch name {
		case "github":
			Register(&GitHub{Config: cfg})
		case "gitlab":
			Register(&GitLab{Config: cfg})
		}
		on = append(on, name)
	}
	return on
}

// DefaultSecretEnv is the variable a host's secret is read from when the config
// does not name one.
func DefaultSecretEnv(host string) string { return envName(host, "CLIENT_SECRET") }

func envName(host, suffix string) string {
	return strings.ToUpper(host) + "_" + suffix
}
