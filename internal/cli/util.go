package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/dougkirkley/dross/internal/config"
)

// storePath is where session handoff briefs and artifacts live.
func storePath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	p := configDir + "/dross/sessions"
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
	Lead             config.Provider
	Worker           config.Provider
	WorkerEnabled    bool
	Reviewer         config.Provider
	ReviewerEnabled  bool
	Compactor        config.Provider
	CompactorEnabled bool
	CompactAt        int
	Chat             config.Provider
	ChatEnabled      bool
	File             *config.File
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

	// Compactor slot: config file's compactor block plus compactor-* flags.
	var cfile config.File
	if file.Compactor != nil {
		cfile = config.File{
			Provider: file.Compactor.Provider,
			Model:    file.Compactor.Model,
			BaseURL:  file.Compactor.BaseURL,
			APIKey:   file.Compactor.APIKey,
		}
	}
	cpFlag, cpChanged := flagStr(cmd, "compactor-provider")
	cmFlag, cmChanged := flagStr(cmd, "compactor-model")
	cbFlag, cbChanged := flagStr(cmd, "compactor-base-url")
	ckFlag, ckChanged := flagStr(cmd, "compactor-api-key")
	comp := config.Resolve(config.Layers{
		File:            cfile,
		ProviderFlag:    cpFlag,
		ProviderDefault: "openai",
		ProviderChanged: cpChanged,
		ModelFlag:       cmFlag,
		ModelDefault:    "",
		ModelChanged:    cmChanged,
		BaseURLFlag:     cbFlag,
		BaseURLChanged:  cbChanged,
		APIKeyFlag:      ckFlag,
		APIKeyChanged:   ckChanged,
	})

	// Chat slot: the config file's chat block, filled in from the lead.
	//
	// Every field is optional and inherits, because the common deployment is
	// one provider at one endpoint and the only thing worth changing for chat
	// is which model answers:
	//
	//     "chat": {"model": "gemma2:9b"}
	//
	// A whole second backend is expressible too — name a provider or a base_url
	// and that is what chat gets — but nobody should have to restate an API key
	// and an endpoint to move one job onto a different model. No chat-* flags:
	// this is a served deployment's routing decision, described once in the
	// file that describes the deployment, not per invocation.
	chat := chatSlot(file.Chat, lead)

	return resolved{
		Lead:             lead,
		Worker:           worker,
		WorkerEnabled:    worker.Model != "",
		Reviewer:         rev,
		ReviewerEnabled:  rev.Model != "" && (file.Review == nil || *file.Review),
		Compactor:        comp,
		CompactorEnabled: comp.Model != "",
		CompactAt:        file.CompactAt,
		Chat:             chat,
		ChatEnabled:      file.Chat != nil && chat.Model != "",
		File:             file,
	}, nil
}

// chatSlot resolves the chat backend from its config block, inheriting anything
// it does not state from the lead.
//
// The endpoint and key are only inherited when chat is on the lead's provider.
// A chat block that names a different provider is describing a different
// service, and handing it the lead's base URL and API key would point, say,
// anthropic at a local ollama endpoint with "ollama" as its key — a
// confidently wrong config that fails at the first turn instead of at startup.
func chatSlot(block *config.Provider, lead config.Provider) config.Provider {
	if block == nil {
		return config.Provider{}
	}
	out := *block
	sameProvider := out.Provider == "" || out.Provider == lead.Provider
	if out.Provider == "" {
		out.Provider = lead.Provider
	}
	if out.Model == "" {
		out.Model = lead.Model
	}
	if sameProvider {
		if out.BaseURL == "" {
			out.BaseURL = lead.BaseURL
		}
		if out.APIKey == "" {
			out.APIKey = lead.APIKey
		}
	}
	return out
}
