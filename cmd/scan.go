package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/moveeeax/aws-lambda-runtime-eol/internal/awsx"
	"github.com/moveeeax/aws-lambda-runtime-eol/internal/eol"
	"github.com/moveeeax/aws-lambda-runtime-eol/internal/report"
)

type scanOpts struct {
	region     string
	regions    []string
	allRegions bool
	versions   string
	within     int
	output     string
	failOn     string
	input      string
	eolData    string
	eolSource  string
	nowStr     string
}

// AWS and network entry points, swapped out in tests so they run offline.
var (
	listFunctionsFn = awsx.ListFunctions
	listRegionsFn   = awsx.ListRegions
	endOfLifeURL    = eol.EndOfLifeURL
	httpClient      = http.DefaultClient
)

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
	f.StringSliceVar(&o.regions, "regions", nil, "comma-separated AWS regions to scan, e.g. eu-west-1,us-east-1")
	f.BoolVar(&o.allRegions, "all-regions", false, "scan every region enabled for the account (ec2:DescribeRegions)")
	f.StringVar(&o.versions, "versions", "latest", "function versions to scan: latest ($LATEST only) or all (every published version)")
	f.IntVar(&o.within, "within", 90, "warn on runtimes within this many days of EOL")
	f.StringVar(&o.output, "output", "table", "output format: table or json")
	f.StringVar(&o.failOn, "fail-on", "", "exit non-zero if any finding is at least this severe: high or medium")
	f.StringVar(&o.input, "input", "", "read functions from a JSON file instead of AWS (fully offline)")
	f.StringVar(&o.eolSource, "eol-source", "builtin", "EOL dates: builtin, endoflife (live from endoflife.date, falls back to builtin) or file:<path>")
	f.StringVar(&o.eolData, "eol-data", "", "same as --eol-source file:<path>")
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
	if o.versions != "latest" && o.versions != "all" {
		return fmt.Errorf("invalid --versions %q (want latest or all)", o.versions)
	}
	set := 0
	for _, b := range []bool{o.region != "", len(o.regions) > 0, o.allRegions} {
		if b {
			set++
		}
	}
	if set > 1 {
		return fmt.Errorf("use only one of --region, --regions and --all-regions")
	}
	source := o.eolSource
	if o.eolData != "" {
		if cmd.Flags().Changed("eol-source") {
			return fmt.Errorf("use either --eol-data or --eol-source, not both")
		}
		source = "file:" + o.eolData
	}

	now := time.Now().UTC()
	if o.nowStr != "" {
		parsed, err := time.Parse(eol.DateLayout, o.nowStr)
		if err != nil {
			return fmt.Errorf("invalid --now %q: %w", o.nowStr, err)
		}
		now = parsed.UTC()
	}

	table, usedSource, err := loadTable(cmd.Context(), source, cmd.ErrOrStderr())
	if err != nil {
		return err
	}

	funcs, scope, err := loadFunctions(cmd.Context(), o)
	if err != nil {
		return err
	}

	findings := table.Classify(funcs, now, o.within)
	meta := report.Meta{Scope: scope, WithinDays: o.within, EOLSource: usedSource}

	if o.output == "json" {
		if err := report.JSON(cmd.OutOrStdout(), meta, findings); err != nil {
			return err
		}
	} else {
		if err := report.Table(cmd.OutOrStdout(), meta, findings); err != nil {
			return err
		}
	}

	return gate(o.failOn, findings)
}

// loadTable resolves --eol-source and returns the table plus the source that
// was actually used. A failed endoflife.date fetch falls back to the built-in
// table with a warning on stderr, and the returned source reads "builtin".
func loadTable(ctx context.Context, source string, stderr io.Writer) (*eol.Table, string, error) {
	switch {
	case source == "builtin":
		t, err := eol.NewDefaultTable()
		return t, source, err
	case source == "endoflife":
		t, err := eol.FetchEndOfLife(ctx, httpClient, endOfLifeURL)
		if err == nil {
			return t, source, nil
		}
		fmt.Fprintf(stderr, "warning: %v; using built-in EOL table (as of %s)\n", err, eol.TableAsOf)
		t, err = eol.NewDefaultTable()
		return t, "builtin", err
	case strings.HasPrefix(source, "file:") && len(source) > len("file:"):
		t, err := eol.LoadTable(strings.TrimPrefix(source, "file:"))
		return t, source, err
	default:
		return nil, "", fmt.Errorf("invalid --eol-source %q (want builtin, endoflife or file:<path>)", source)
	}
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
	allVersions := o.versions == "all"

	regions := o.regions
	if o.allRegions {
		var err error
		regions, err = listRegionsFn(ctx)
		if err != nil {
			return nil, "", fmt.Errorf("list regions: %w", err)
		}
		if len(regions) == 0 {
			return nil, "", fmt.Errorf("list regions: no enabled regions returned")
		}
	}
	if len(regions) == 0 {
		funcs, err := listFunctionsFn(ctx, o.region, allVersions)
		if err != nil {
			return nil, "", fmt.Errorf("list functions: %w", err)
		}
		scope := o.region
		if scope == "" {
			scope = "default region"
		}
		return funcs, "region " + scope, nil
	}

	// A failed region aborts the scan: a partial result would under-report
	// and could let a --fail-on gate pass.
	var all []eol.Function
	for _, r := range regions {
		funcs, err := listFunctionsFn(ctx, r, allVersions)
		if err != nil {
			return nil, "", fmt.Errorf("list functions in %s: %w", r, err)
		}
		all = append(all, funcs...)
	}
	label := "regions "
	if len(regions) == 1 {
		label = "region "
	}
	return all, label + strings.Join(regions, ", "), nil
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
