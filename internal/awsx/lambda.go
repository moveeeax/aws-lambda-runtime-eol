// Package awsx wraps the AWS SDK so the rest of the tool never imports it
// directly. It enumerates Lambda functions per region and the account's enabled
// regions; everything else in the tool works on the returned []eol.Function and
// needs no AWS access.
package awsx

import (
	"context"
	"fmt"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"

	"github.com/moveeeax/aws-lambda-runtime-eol/internal/eol"
)

// regionsAPI is the slice of the EC2 client used to list regions.
type regionsAPI interface {
	DescribeRegions(context.Context, *ec2.DescribeRegionsInput, ...func(*ec2.Options)) (*ec2.DescribeRegionsOutput, error)
}

func loadConfig(ctx context.Context, region string) (aws.Config, error) {
	var opts []func(*awsconfig.LoadOptions) error
	if region != "" {
		opts = append(opts, awsconfig.WithRegion(region))
	}
	return awsconfig.LoadDefaultConfig(ctx, opts...)
}

// ListFunctions enumerates every Lambda function in the given region using the
// caller's default credential chain, paginating through ListFunctions. With
// allVersions it passes FunctionVersion=ALL, so each published version is
// returned as its own entry alongside $LATEST.
func ListFunctions(ctx context.Context, region string, allVersions bool) ([]eol.Function, error) {
	cfg, err := loadConfig(ctx, region)
	if err != nil {
		return nil, err
	}
	return listFunctions(ctx, lambda.NewFromConfig(cfg), cfg.Region, allVersions)
}

func listFunctions(ctx context.Context, client lambda.ListFunctionsAPIClient, region string, allVersions bool) ([]eol.Function, error) {
	in := &lambda.ListFunctionsInput{}
	if allVersions {
		in.FunctionVersion = lambdatypes.FunctionVersionAll
	}
	var out []eol.Function
	paginator := lambda.NewListFunctionsPaginator(client, in)
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, fn := range page.Functions {
			out = append(out, eol.Function{
				Name:         aws.ToString(fn.FunctionName),
				Runtime:      string(fn.Runtime),
				Region:       region,
				Version:      aws.ToString(fn.Version),
				LastModified: aws.ToString(fn.LastModified),
			})
		}
	}
	return out, nil
}

// ListRegions returns the regions enabled for the account (DescribeRegions
// without AllRegions, so opt-in regions that are not enabled are skipped),
// sorted by name. The call is made from the SDK's resolved default region.
func ListRegions(ctx context.Context) ([]string, error) {
	cfg, err := loadConfig(ctx, "")
	if err != nil {
		return nil, err
	}
	if cfg.Region == "" {
		return nil, fmt.Errorf("no region to call ec2:DescribeRegions from: set AWS_REGION or a profile region")
	}
	return listRegions(ctx, ec2.NewFromConfig(cfg))
}

func listRegions(ctx context.Context, client regionsAPI) ([]string, error) {
	out, err := client.DescribeRegions(ctx, &ec2.DescribeRegionsInput{})
	if err != nil {
		return nil, err
	}
	regions := make([]string, 0, len(out.Regions))
	for _, r := range out.Regions {
		if name := aws.ToString(r.RegionName); name != "" {
			regions = append(regions, name)
		}
	}
	sort.Strings(regions)
	return regions, nil
}
