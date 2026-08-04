package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dougkirkley/dross/internal/catalog"
	"github.com/dougkirkley/dross/internal/config"
)

// newProviderCmd is `dross provider`: name a model backend dross does not ship
// knowledge of, so it can be referred to like any built-in one.
//
// The compiled catalog covers services everyone can reach. It can never name a
// private endpoint — a model server on a home network, a company gateway —
// and until now such a server could be pointed at per invocation but not
// called what it is: absent from the picker, unusable by name in a worker or
// chat block.
//
// A command rather than a documented config block, because the config file is
// dross's to write. Nobody should have to learn a schema to register a URL
// they already know.
func newProviderCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "provider",
		Short: "Manage model providers (list, add, remove)",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List providers, configured and built-in",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfgPath, _ := cmd.Flags().GetString("config")
			file := loadProviderFile(cfgPath)
			catalog.SetUserProviders(userProviders(file))
			out := cmd.OutOrStdout()

			ids := make([]string, 0, len(file.Providers))
			for id := range file.Providers {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			if len(ids) == 0 {
				fmt.Fprintln(out, "no configured providers · add: dross provider add <id> <base-url>")
			} else {
				fmt.Fprintln(out, "configured (editable):")
				for _, id := range ids {
					p, _ := catalog.Get(id)
					fmt.Fprintf(out, "  %-14s %s%s\n", id, p.BaseURL, localNote(p))
				}
			}
			fmt.Fprintln(out, "built-in:")
			for _, id := range catalog.IDs() {
				if _, ours := file.Providers[id]; ours {
					continue
				}
				p, _ := catalog.Get(id)
				where := p.BaseURL
				if where == "" {
					where = "(native backend)"
				}
				fmt.Fprintf(out, "  %-14s %s%s\n", id, where, localNote(p))
			}
			return nil
		},
	})

	var (
		name, impl, apiKey, apiKeyEnv, local string
		models                               []string
	)
	add := &cobra.Command{
		Use:   "add <id> <base-url>",
		Short: "Add or update a model provider",
		Long: "Register a model backend so it can be used by name.\n\n" +
			"  a local server:   dross provider add homelab https://ollama.example.com/v1 --local\n" +
			"  a hosted one:     dross provider add acme https://api.acme.ai/v1 --api-key-env ACME_KEY\n" +
			"  repoint a known:  dross provider add ollama https://ollama.example.com/v1\n\n" +
			"Models are discovered from the endpoint unless --model is given: a\n" +
			"hand-written list of a local server's models is stale the moment\n" +
			"anything is pulled.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, _ := cmd.Flags().GetString("config")
			id, base := args[0], args[1]
			if strings.TrimSpace(id) == "" {
				return fmt.Errorf("provider id must not be empty")
			}
			if local != "" && !catalog.ValidLocalKind(local) {
				return fmt.Errorf("unknown local kind %q; one of: %s", local, localKindList())
			}
			def := config.ProviderDef{
				Name: name, Provider: impl, BaseURL: base,
				APIKey: apiKey, APIKeyEnv: apiKeyEnv, Local: local, Models: models,
			}

			path := configFilePath(cfgPath)
			file := loadProviderFile(cfgPath)
			if file.Providers == nil {
				file.Providers = map[string]config.ProviderDef{}
			}
			file.Providers[id] = def
			if err := setConfigKey(path, "providers", file.Providers); err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "added provider %q -> %s in %s\n", id, base, path)

			// Say whether it answered. Registering a URL that turns out to be
			// wrong is the whole failure mode here, and finding out at the next
			// prompt is finding out too late.
			catalog.SetUserProviders(userProviders(file))
			p, _ := catalog.Get(id)
			ids := fetchModelIDs(id, p.BaseURL, p.APIKey())
			if len(ids) == 0 {
				fmt.Fprintf(out, "no models listed — check the URL is reachable and ends in /v1\n")
				return nil
			}
			fmt.Fprintf(out, "%d models:\n", len(ids))
			describeModels(cmd.Context(), out, p, ids)
			return nil
		},
	}
	add.Flags().StringVar(&name, "name", "", "display name (defaults to the id)")
	add.Flags().StringVar(&impl, "provider", "", "backend: openai (default), anthropic, gemini, bedrock, …")
	add.Flags().StringVar(&apiKey, "api-key", "", "API key, stored in the config file")
	add.Flags().StringVar(&apiKeyEnv, "api-key-env", "", "environment variable holding the API key")
	add.Flags().StringVar(&local, "local", "", "local server kind: "+localKindList()+" (no key; models discovered)")
	add.Flags().StringArrayVar(&models, "model", nil, "model id (repeatable); omit to discover from the endpoint")
	cmd.AddCommand(add)

	cmd.AddCommand(&cobra.Command{
		Use:     "remove <id>",
		Aliases: []string{"rm"},
		Short:   "Remove a configured provider",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, _ := cmd.Flags().GetString("config")
			id := args[0]
			path := configFilePath(cfgPath)
			file := loadProviderFile(cfgPath)
			if _, ok := file.Providers[id]; !ok {
				return fmt.Errorf("no configured provider %q", id)
			}
			delete(file.Providers, id)
			if err := setConfigKey(path, "providers", file.Providers); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "removed provider %q from %s\n", id, path)
			// A built-in of the same id comes back rather than disappearing.
			if _, ok := catalog.Get(id); ok {
				fmt.Fprintf(out, "the built-in %q is in use again\n", id)
			}
			return nil
		},
	})

	return cmd
}

func loadProviderFile(cfgPath string) *config.File {
	f, _, err := config.Load(cfgPath)
	if err != nil || f == nil {
		return &config.File{}
	}
	return f
}

// userProviders converts the config's provider defs into catalog entries.
func userProviders(f *config.File) map[string]catalog.Provider {
	if len(f.Providers) == 0 {
		return nil
	}
	out := make(map[string]catalog.Provider, len(f.Providers))
	for id, d := range f.Providers {
		out[id] = catalog.Provider{
			ID: id, Name: d.Name, Impl: d.Provider, BaseURL: d.BaseURL,
			APIKeyEnv: d.APIKeyEnv, Key: d.APIKey,
			Local: catalog.LocalKind(d.Local), Models: d.Models,
		}
	}
	return out
}

func localKindList() string {
	names := make([]string, 0, 4)
	for _, k := range catalog.LocalKinds() {
		names = append(names, string(k))
	}
	return strings.Join(names, ", ")
}

func localNote(p catalog.Provider) string {
	if p.IsLocal() {
		return "  (" + string(p.Local) + ")"
	}
	if p.APIKeyEnv != "" {
		if os.Getenv(p.APIKeyEnv) == "" {
			return "  ($" + p.APIKeyEnv + " unset)"
		}
		return "  ($" + p.APIKeyEnv + ")"
	}
	return ""
}

// describeModels lists a provider's models, annotated with whatever its kind
// can tell us.
//
// The annotation that matters is tool calling, because a Code session is a
// tool loop and a model that cannot call one cannot do the work. Saying it
// here means picking a model for the wrong job is a thing you find out while
// choosing rather than at the first prompt.
//
// Only ollama can answer; for anything else the list is printed plainly rather
// than padded with unknowns.
func describeModels(ctx context.Context, out io.Writer, p catalog.Provider, ids []string) {
	if p.Local != catalog.Ollama {
		for _, id := range ids {
			fmt.Fprintf(out, "  %s\n", id)
		}
		return
	}
	for _, id := range ids {
		info := p.Describe(ctx, id)
		if !info.Known {
			fmt.Fprintf(out, "  %s\n", id)
			continue
		}
		var notes []string
		if info.Params != "" {
			notes = append(notes, info.Params)
		}
		if info.Quant != "" {
			notes = append(notes, info.Quant)
		}
		// Stated as an absence, not a warning: a model without tools is a fine
		// chat model, and only wrong for a Code session.
		if !info.Tools {
			notes = append(notes, "no tools — chat only")
		}
		if info.Thinking {
			notes = append(notes, "thinking")
		}
		if len(notes) == 0 {
			fmt.Fprintf(out, "  %s\n", id)
			continue
		}
		fmt.Fprintf(out, "  %-22s %s\n", id, strings.Join(notes, " · "))
	}
}
