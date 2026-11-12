package awsx

import (
	"context"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

// fakeLambda serves two pages and records every request it saw.
type fakeLambda struct {
	inputs []lambda.ListFunctionsInput
}

func (f *fakeLambda) ListFunctions(_ context.Context, in *lambda.ListFunctionsInput, _ ...func(*lambda.Options)) (*lambda.ListFunctionsOutput, error) {
	f.inputs = append(f.inputs, *in)
	if in.Marker == nil {
		return &lambda.ListFunctionsOutput{
			Functions: []lambdatypes.FunctionConfiguration{
				{FunctionName: aws.String("api"), Runtime: lambdatypes.RuntimeNodejs18x, Version: aws.String("$LATEST")},
				{FunctionName: aws.String("api"), Runtime: lambdatypes.RuntimeNodejs16x, Version: aws.String("3")},
			},
			NextMarker: aws.String("page2"),
		}, nil
	}
	return &lambda.ListFunctionsOutput{
		Functions: []lambdatypes.FunctionConfiguration{
			{FunctionName: aws.String("cron"), Runtime: lambdatypes.RuntimePython38, Version: aws.String("$LATEST")},
		},
	}, nil
}

func TestListFunctionsAllVersions(t *testing.T) {
	fake := &fakeLambda{}
	got, err := listFunctions(context.Background(), fake, "eu-west-1", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d functions across pages, want 3", len(got))
	}
	for _, in := range fake.inputs {
		if in.FunctionVersion != lambdatypes.FunctionVersionAll {
			t.Errorf("FunctionVersion = %q, want ALL", in.FunctionVersion)
		}
	}
	if got[1].Version != "3" || got[1].Runtime != "nodejs16.x" || got[1].Region != "eu-west-1" {
		t.Errorf("published version entry = %+v", got[1])
	}
}

func TestListFunctionsLatestOnly(t *testing.T) {
	fake := &fakeLambda{}
	if _, err := listFunctions(context.Background(), fake, "us-east-1", false); err != nil {
		t.Fatal(err)
	}
	if fake.inputs[0].FunctionVersion != "" {
		t.Errorf("FunctionVersion = %q, want unset for latest", fake.inputs[0].FunctionVersion)
	}
}

type fakeEC2 struct{ in *ec2.DescribeRegionsInput }

func (f *fakeEC2) DescribeRegions(_ context.Context, in *ec2.DescribeRegionsInput, _ ...func(*ec2.Options)) (*ec2.DescribeRegionsOutput, error) {
	f.in = in
	return &ec2.DescribeRegionsOutput{Regions: []ec2types.Region{
		{RegionName: aws.String("us-east-1")},
		{RegionName: aws.String("eu-west-1")},
		{RegionName: aws.String("ap-south-1")},
	}}, nil
}

func TestListRegionsSortedEnabledOnly(t *testing.T) {
	fake := &fakeEC2{}
	got, err := listRegions(context.Background(), fake)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"ap-south-1", "eu-west-1", "us-east-1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("regions = %v, want %v", got, want)
	}
	if fake.in.AllRegions != nil {
		t.Error("AllRegions must stay unset so disabled opt-in regions are skipped")
	}
}
