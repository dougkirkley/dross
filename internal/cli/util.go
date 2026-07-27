package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/dougkirkley/holocron/internal/config"
)

// storePath is where session handoff briefs and artifacts live.
func storePath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	p := configDir + "/holocron/sessions"
	if err := os.MkdirAll(p, 0o755); err != nil {
		return "", err
	}
	return p, nil
}

func mustStorePath() (string, error) {
	p, err := storePath()
	if err != nil {
		return "", fmt.Errorf("storage dir: %w", err)
	}
	return p, nil
}

// resolved is the effective provider configuration after merging config file,
// environment, and flags.
type resolved struct {
	Lead            config.Provider
	Worker          config.Provider
	WorkerEnabled   bool
	Reviewer        config.Provider
	ReviewerEnabled bool
	File            *config.File
}

// flagStr returns a string flag's value and whether it was explicitly set. A
// flag that does not exist on the command yields ("", false).
func flagStr(cmd *cobra.Command, name string) (string, bool) {
	f := cmd.Flags().Lookup(name)
	if f == nil {
		return "", false
	}
	v, _ := cmd.Flags().GetString(name)
	return v, f.Changed
}

// resolveProviders loads the config file (honoring the persistent --config
// flag) and merges it with environment variables and command flags to produce
// the effective lead and (optional) worker provider settings. Precedence per
// field: an explicitly-set flag > environment variable > config file > default.
func resolveProviders(cmd *cobra.Command, provDefault, modelDefault string) (resolved, error) {
	cfgPath, _ := cmd.Flags().GetString("config")
	file, path, err := config.Load(cfgPath)
	if err != nil {
		return resolved{}, fmt.Errorf("config %s: %w", path, err)
	}

	provFlag, provChanged := flagStr(cmd, "provider")
	modelFlag, modelChanged := flagStr(cmd, "model")
	baseFlag, baseChanged := flagStr(cmd, "base-url")
	keyFlag, keyChanged := flagStr(cmd, "api-key")

	lead := config.Resolve(config.Layers{
		File:            *file,
		ProviderFlag:    provFlag,
		ProviderDefault: provDefault,
		ProviderChanged: provChanged,
		ModelFlag:       modelFlag,
		ModelDefault:    modelDefault,
		ModelChanged:    modelChanged,
		BaseURLFlag:     baseFlag,
		BaseURLChanged:  baseChanged,
		APIKeyFlag:      keyFlag,
		APIKeyChanged:   keyChanged,
	})

	// Worker slot: config file's worker block plus worker-* flags.
	var wfile config.File
	if file.Worker != nil {
		wfile = config.File{
			Provider: file.Worker.Provider,
			Model:    file.Worker.Model,
			BaseURL:  file.Worker.BaseURL,
			APIKey:   file.Worker.APIKey,
		}
	}
	wpFlag, wpChanged := flagStr(cmd, "worker-provider")
	wmFlag, wmChanged := flagStr(cmd, "worker-model")
	wbFlag, wbChanged := flagStr(cmd, "worker-base-url")
	wkFlag, wkChanged := flagStr(cmd, "worker-api-key")
	worker := config.Resolve(config.Layers{
		File:            wfile,
		ProviderFlag:    wpFlag,
		ProviderDefault: "openai",
		ProviderChanged: wpChanged,
		ModelFlag:       wmFlag,
		ModelDefault:    "",
		ModelChanged:    wmChanged,
		BaseURLFlag:     wbFlag,
		BaseURLChanged:  wbChanged,
		APIKeyFlag:      wkFlag,
		APIKeyChanged:   wkChanged,
	})

	// Reviewer slot: config file's reviewer block plus reviewer-* flags.
	var rfile config.File
	if file.Reviewer != nil {
		rfile = config.File{
			Provider: file.Reviewer.Provider,
			Model:    file.Reviewer.Model,
			BaseURL:  file.Reviewer.BaseURL,
			APIKey:   file.Reviewer.APIKey,
		}
	}
	rpFlag, rpChanged := flagStr(cmd, "reviewer-provider")
	rmFlag, rmChanged := flagStr(cmd, "reviewer-model")
	rbFlag, rbChanged := flagStr(cmd, "reviewer-base-url")
	rkFlag, rkChanged := flagStr(cmd, "reviewer-api-key")
	rev := config.Resolve(config.Layers{
		File:            rfile,
		ProviderFlag:    rpFlag,
		ProviderDefault: "anthropic",
		ProviderChanged: rpChanged,
		ModelFlag:       rmFlag,
		ModelDefault:    "",
		ModelChanged:    rmChanged,
		BaseURLFlag:     rbFlag,
		BaseURLChanged:  rbChanged,
		APIKeyFlag:      rkFlag,
		APIKeyChanged:   rkChanged,
	})

	return resolved{
		Lead:            lead,
		Worker:          worker,
		WorkerEnabled:   worker.Model != "",
		Reviewer:        rev,
		ReviewerEnabled: rev.Model != "",
		File:            file,
	}, nil
}
