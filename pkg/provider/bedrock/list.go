package bedrock

import (
	"context"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	ctrl "github.com/aws/aws-sdk-go-v2/service/bedrock"
	ctrltypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"
)

// ListModels returns the system-defined inference-profile ids available in the
// caller's AWS region (resolved from the default credential chain). These are
// the real, region-correct model ids to pass as the model — e.g. the picker
// shows "us.anthropic.claude-sonnet-4-5-…" in us-* and "eu.…" in eu-*. Returns
// an error if credentials/region are unavailable; callers fall back to the
// catalog list.
func ListModels(ctx context.Context) ([]string, error) {
	awsCfg, err := awscfg.LoadDefaultConfig(ctx, loadOpts()...)
	if err != nil {
		return nil, err
	}
	c := ctrl.NewFromConfig(awsCfg)
	p := ctrl.NewListInferenceProfilesPaginator(c, &ctrl.ListInferenceProfilesInput{
		TypeEquals: ctrltypes.InferenceProfileTypeSystemDefined,
	})
	var ids []string
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, s := range page.InferenceProfileSummaries {
			if s.InferenceProfileId != nil {
				ids = append(ids, aws.ToString(s.InferenceProfileId))
			}
		}
	}
	sort.Strings(ids)
	return ids, nil
}
