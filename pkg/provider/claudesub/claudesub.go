// Package claudesub registers a "claude-sub" provider that authenticates with an
// OAuth bearer token from `dross login` (see internal/subauth) instead of an
// API key — for using a subscription-backed endpoint.
//
// dross ships NO OAuth client and impersonates no vendor: the client id,
// endpoints, the anthropic-beta header value, and any required system-prompt pin
// all come from the user's own config ([oauth]). Import for side effects:
//
//	import _ "github.com/dougkirkley/dross/pkg/provider/claudesub"
package claudesub

import (
	"fmt"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/dougkirkley/dross/internal/config"
	"github.com/dougkirkley/dross/pkg/provider"
	"github.com/dougkirkley/dross/pkg/provider/anthropic"
	"github.com/dougkirkley/dross/internal/subauth"
)

func init() {
	provider.Register("claude-sub", func(cfg provider.Config) (provider.Provider, error) {
		f, _, err := config.Load("")
		if err != nil {
			return nil, fmt.Errorf("claude-sub: config: %w", err)
		}
		if !f.OAuth.Configured() {
			return nil, fmt.Errorf("claude-sub: no [oauth] client in config — see README (subscription)")
		}
		tok, err := subauth.AccessToken(*f.OAuth)
		if err != nil {
			return nil, fmt.Errorf("claude-sub: %w", err)
		}
		if tok == "" {
			return nil, fmt.Errorf("claude-sub: not logged in — run `dross login`")
		}
		opts := []option.RequestOption{option.WithAuthToken(tok)}
		if f.OAuth.BetaHeader != "" {
			opts = append(opts, option.WithHeader("anthropic-beta", f.OAuth.BetaHeader))
		}
		base := f.OAuth.BaseURL
		if cfg.BaseURL != "" {
			base = cfg.BaseURL
		}
		if base != "" {
			opts = append(opts, option.WithBaseURL(base))
		}
		return anthropic.NewWithClientPinned(sdk.NewClient(opts...), "claude-sub", f.OAuth.SystemPin), nil
	})
}
