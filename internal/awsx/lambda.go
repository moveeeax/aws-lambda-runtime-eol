// Package awsx wraps the AWS SDK so the rest of the tool never imports it
// directly. It enumerates Lambda functions in a region; everything else in the
// tool works on the returned []eol.Function and needs no AWS access.
package awsx

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambda"

	"github.com/moveeeax/aws-lambda-runtime-eol/internal/eol"
)

// ListFunctions enumerates every Lambda function in the given region using the
// caller's default credential chain, paginating through ListFunctions.
func ListFunctions(ctx context.Context, region string) ([]eol.Function, error) {
	var opts []func(*awsconfig.LoadOptions) error
	if region != "" {
		opts = append(opts, awsconfig.WithRegion(region))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, err
	}
	client := lambda.NewFromConfig(cfg)

	var out []eol.Function
	paginator := lambda.NewListFunctionsPaginator(client, &lambda.ListFunctionsInput{})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, fn := range page.Functions {
			out = append(out, eol.Function{
				Name:         aws.ToString(fn.FunctionName),
				Runtime:      string(fn.Runtime),
				LastModified: aws.ToString(fn.LastModified),
			})
		}
	}
	return out, nil
}
