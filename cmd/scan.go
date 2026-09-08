package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/moveeeax/aws-lambda-runtime-eol/internal/awsx"
	"github.com/moveeeax/aws-lambda-runtime-eol/internal/eol"
	"github.com/moveeeax/aws-lambda-runtime-eol/internal/report"
)

type scanOpts struct {
	region  string
	within  int
	output  string
	failOn  string
	input   string
	eolData string
	nowStr  string
}

func newScanCmd() *cobra.Command {
	o := &scanOpts{}
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan Lambda functions and classify their runtimes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runScan(cmd, o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.region, "region", "", "AWS region to scan (defaults to the SDK's resolved region)")
	f.IntVar(&o.within, "within", 90, "warn on runtimes within this many days of EOL")
	f.StringVar(&o.output, "output", "table", "output format: table or json")
	f.StringVar(&o.failOn, "fail-on", "", "exit non-zero if any finding is at least this severe: high or medium")
	f.StringVar(&o.input, "input", "", "read functions from a JSON file instead of AWS (fully offline)")
	f.StringVar(&o.eolData, "eol-data", "", "override the embedded EOL table with a JSON file of {runtime: date}")
	f.StringVar(&o.nowStr, "now", "", "reference date YYYY-MM-DD for classification (default: today, UTC)")
	return cmd
}

func runScan(cmd *cobra.Command, o *scanOpts) error {
	if o.output != "table" && o.output != "json" {
		return fmt.Errorf("invalid --output %q (want table or json)", o.output)
	}
	if o.within < 0 {
		return fmt.Errorf("--within must be >= 0")
	}

	now := time.Now().UTC()
	if o.nowStr != "" {
		parsed, err := time.Parse(eol.DateLayout, o.nowStr)
		if err != nil {
			return fmt.Errorf("invalid --now %q: %w", o.nowStr, err)
		}
		now = parsed.UTC()
	}

	table, err := loadTable(o.eolData)
	if err != nil {
		return err
	}

	funcs, scope, err := loadFunctions(cmd.Context(), o)
	if err != nil {
		return err
	}

	findings := table.Classify(funcs, now, o.within)

	if o.output == "json" {
		if err := report.JSON(cmd.OutOrStdout(), scope, o.within, findings); err != nil {
			return err
		}
	} else {
		if err := report.Table(cmd.OutOrStdout(), scope, o.within, findings); err != nil {
			return err
		}
	}

	return gate(o.failOn, findings)
}

func loadTable(path string) (*eol.Table, error) {
	if path != "" {
		return eol.LoadTable(path)
	}
	return eol.NewDefaultTable()
}

// loadFunctions returns the functions to classify and a human label for the
// scope, reading either an --input file (offline) or live AWS.
func loadFunctions(ctx context.Context, o *scanOpts) ([]eol.Function, string, error) {
	if o.input != "" {
		funcs, err := readInputFile(o.input)
		if err != nil {
			return nil, "", err
		}
		return funcs, "input file " + o.input, nil
	}
	funcs, err := awsx.ListFunctions(ctx, o.region)
	if err != nil {
		return nil, "", fmt.Errorf("list functions: %w", err)
	}
	scope := o.region
	if scope == "" {
		scope = "default region"
	}
	return funcs, "region " + scope, nil
}

// readInputFile accepts either a bare JSON array of functions or an object of
// the form {"functions": [...]}.
func readInputFile(path string) ([]eol.Function, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var wrapped struct {
		Functions []eol.Function `json:"functions"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil && wrapped.Functions != nil {
		return wrapped.Functions, nil
	}
	var arr []eol.Function
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, fmt.Errorf("parse input %s: %w", path, err)
	}
	return arr, nil
}

// gate applies --fail-on: exit code 2 when a finding meets the threshold.
func gate(failOn string, findings []eol.Finding) error {
	if failOn == "" {
		return nil
	}
	threshold, err := eol.ParseSeverity(failOn)
	if err != nil {
		return err
	}
	if eol.Worst(findings).AtLeast(threshold) {
		return &exitError{code: 2}
	}
	return nil
}
