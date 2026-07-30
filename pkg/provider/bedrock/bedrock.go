// Package bedrock is the AWS Bedrock backend for the provider registry. Claude
// model ids use the Anthropic Messages API over Bedrock's transport (reusing the
// anthropic package, with prompt caching); every other model (Llama, Nova,
// Mistral, Cohere, …) uses Bedrock's unified Converse API. Import it for side
// effects to register the "bedrock" provider:
//
//	import _ "github.com/dougkirkley/dross/pkg/provider/bedrock"
//
// Credentials and region come from the standard AWS default chain
// (AWS_REGION / AWS_DEFAULT_REGION, AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY,
// AWS_PROFILE, SSO, instance roles, ...). The model must be a Bedrock model id
// or inference-profile id, e.g. "us.anthropic.claude-opus-4-5-20251101-v1:0".
package bedrock

import (
	"context"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/bedrock"
	"github.com/anthropics/anthropic-sdk-go/option"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"

	"github.com/dougkirkley/dross/pkg/provider"
	"github.com/dougkirkley/dross/pkg/provider/anthropic"
)

func init() {
	provider.Register("bedrock", func(cfg provider.Config) (provider.Provider, error) {
		opts := []option.RequestOption{
			// Resolves credentials and region from the AWS default chain.
			bedrock.WithLoadDefaultConfig(context.Background()),
		}
		if cfg.BaseURL != "" {
			opts = append(opts, option.WithBaseURL(cfg.BaseURL))
		}
		p := &prov{claude: anthropic.NewWithClient(sdk.NewClient(opts...), "bedrock")}
		// Converse client for non-Claude models (Llama, Nova, Mistral, …).
		if awsCfg, err := awscfg.LoadDefaultConfig(context.Background()); err == nil {
			p.brc = bedrockruntime.NewFromConfig(awsCfg)
		}
		return p, nil
	})
}

// prov dispatches per model: Claude ids use the anthropic-over-bedrock backend
// (prompt caching); everything else uses the unified Converse API.
type prov struct {
	claude provider.Provider
	brc    *bedrockruntime.Client
}

func (p *prov) Name() string { return "bedrock" }

func (p *prov) NewSession(cfg provider.SessionConfig) provider.Session {
	if isAnthropicModel(cfg.Model) || p.brc == nil {
		return p.claude.NewSession(cfg)
	}
	return newConverseSession(p.brc, cfg)
}
