// Package bedrock is the AWS Bedrock backend for the provider registry. It
// speaks the Anthropic Messages API over Bedrock's transport, reusing the
// anthropic package's session logic. Import it for side effects to register the
// "bedrock" provider:
//
//	import _ "github.com/dougkirkley/ergon/pkg/provider/bedrock"
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

	"github.com/dougkirkley/ergon/pkg/provider"
	"github.com/dougkirkley/ergon/pkg/provider/anthropic"
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
		return anthropic.NewWithClient(sdk.NewClient(opts...), "bedrock"), nil
	})
}
