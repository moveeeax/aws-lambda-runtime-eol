package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/moveeeax/aws-lambda-runtime-eol/internal/eol"
	"github.com/moveeeax/aws-lambda-runtime-eol/internal/report"
)

// runCLISplit is runCLI with stdout and stderr kept apart.
func runCLISplit(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := newRootCmd()
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(args)
	err = root.Execute()
	return out.String(), errb.String(), err
}

func decodeDoc(t *testing.T, out string) report.Document {
	t.Helper()
	var doc report.Document
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	return doc
}

// withEndOfLife points the endoflife source at a test server for one test.
func withEndOfLife(t *testing.T, h http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	oldURL, oldClient := endOfLifeURL, httpClient
	endOfLifeURL, httpClient = srv.URL, srv.Client()
	t.Cleanup(func() { endOfLifeURL, httpClient = oldURL, oldClient })
}

func TestScanEOLSourceDefaultBuiltin(t *testing.T) {
	out, _, err := runCLISplit(t, "scan", "--input", writeFixture(t), "--output", "json", "--now", "2025-07-03")
	if err != nil {
		t.Fatal(err)
	}
	if doc := decodeDoc(t, out); doc.EOLSource != "builtin" {
		t.Errorf("eolSource = %q, want builtin", doc.EOLSource)
	}
}

func TestScanEOLSourceEndOfLife(t *testing.T) {
	withEndOfLife(t, func(w http.ResponseWriter, _ *http.Request) {
		// nodejs18.x moved out of the 90-day window.
		w.Write([]byte(`{"result":{"releases":[{"name":"nodejs18.x","eoasFrom":"2026-06-30"}]}}`))
	})
	out, stderr, err := runCLISplit(t, "scan", "--input", writeFixture(t), "--output", "json",
		"--now", "2025-07-03", "--eol-source", "endoflife")
	if err != nil {
		t.Fatal(err)
	}
	if stderr != "" {
		t.Errorf("unexpected stderr: %s", stderr)
	}
	doc := decodeDoc(t, out)
	if doc.EOLSource != "endoflife" {
		t.Errorf("eolSource = %q, want endoflife", doc.EOLSource)
	}
	if doc.Summary.Warning != 0 || doc.Summary.Expired != 1 {
		t.Errorf("summary = %+v, want nodejs18.x OK under live data, python3.8 still expired", doc.Summary)
	}
}

func TestScanEOLSourceEndOfLifeFallsBack(t *testing.T) {
	withEndOfLife(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusBadGateway)
	})
	out, stderr, err := runCLISplit(t, "scan", "--input", writeFixture(t), "--output", "json",
		"--now", "2025-07-03", "--eol-source", "endoflife")
	if err != nil {
		t.Fatalf("fallback should not fail the scan: %v", err)
	}
	if !strings.Contains(stderr, "warning:") || !strings.Contains(stderr, eol.TableAsOf) {
		t.Errorf("stderr = %q, want a fallback warning naming the table date", stderr)
	}
	doc := decodeDoc(t, out)
	if doc.EOLSource != "builtin" {
		t.Errorf("eolSource = %q after fallback, want builtin", doc.EOLSource)
	}
	if doc.Summary.Warning != 1 {
		t.Errorf("summary = %+v, want built-in classification", doc.Summary)
	}
}

func TestScanEOLSourceFileAndAlias(t *testing.T) {
	dir := t.TempDir()
	table := filepath.Join(dir, "eol.json")
	if err := os.WriteFile(table, []byte(`{"nodejs20.x":"2025-01-01"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"--eol-source", "file:" + table}, {"--eol-data", table}} {
		full := append([]string{"scan", "--input", writeFixture(t), "--output", "json", "--now", "2025-07-03"}, args...)
		out, _, err := runCLISplit(t, full...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		doc := decodeDoc(t, out)
		if doc.EOLSource != "file:"+table {
			t.Errorf("%v: eolSource = %q, want file:%s", args, doc.EOLSource, table)
		}
		// Only nodejs20.x is in the file, and it is expired there.
		if doc.Summary.Expired != 1 || doc.Summary.OK != 2 {
			t.Errorf("%v: summary = %+v, want 1 expired / 2 ok", args, doc.Summary)
		}
	}
}

func TestScanEOLSourceInvalid(t *testing.T) {
	fx := writeFixture(t)
	for _, args := range [][]string{
		{"--eol-source", "yaml"},
		{"--eol-source", "file:"},
		{"--eol-source", "builtin", "--eol-data", "x.json"},
	} {
		if _, _, err := runCLISplit(t, append([]string{"scan", "--input", fx}, args...)...); err == nil {
			t.Errorf("%v: expected an error", args)
		}
	}
}

// fakeAWS replaces the AWS entry points for one test.
type fakeAWS struct {
	regions     []string
	byRegion    map[string][]eol.Function
	failRegion  string
	calls       []string
	allVersions []bool
}

func (f *fakeAWS) install(t *testing.T) {
	t.Helper()
	oldList, oldRegions := listFunctionsFn, listRegionsFn
	listFunctionsFn = func(_ context.Context, region string, allVersions bool) ([]eol.Function, error) {
		f.calls = append(f.calls, region)
		f.allVersions = append(f.allVersions, allVersions)
		if region == f.failRegion {
			return nil, errors.New("AccessDenied")
		}
		return f.byRegion[region], nil
	}
	listRegionsFn = func(context.Context) ([]string, error) { return f.regions, nil }
	t.Cleanup(func() { listFunctionsFn, listRegionsFn = oldList, oldRegions })
}

func newFakeAWS() *fakeAWS {
	return &fakeAWS{
		regions: []string{"eu-west-1", "us-east-1"},
		byRegion: map[string][]eol.Function{
			"eu-west-1": {
				{Name: "api", Runtime: "nodejs20.x", Region: "eu-west-1", Version: "$LATEST"},
				{Name: "api", Runtime: "python3.8", Region: "eu-west-1", Version: "4"},
			},
			"us-east-1": {
				{Name: "edge", Runtime: "nodejs18.x", Region: "us-east-1", Version: "$LATEST"},
			},
		},
	}
}

func TestScanRegionsAndVersions(t *testing.T) {
	fake := newFakeAWS()
	fake.install(t)
	out, _, err := runCLISplit(t, "scan", "--regions", "eu-west-1,us-east-1", "--versions", "all",
		"--output", "json", "--now", "2025-07-03")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fake.calls, []string{"eu-west-1", "us-east-1"}) {
		t.Errorf("regions scanned = %v", fake.calls)
	}
	for _, v := range fake.allVersions {
		if !v {
			t.Error("--versions all must request every published version")
		}
	}
	doc := decodeDoc(t, out)
	if doc.GeneratedFor != "regions eu-west-1, us-east-1" {
		t.Errorf("generatedFor = %q", doc.GeneratedFor)
	}
	if doc.Summary.Total != 3 || doc.Summary.Expired != 1 || doc.Summary.Warning != 1 {
		t.Errorf("summary = %+v", doc.Summary)
	}
	if c := doc.Summary.Regions["eu-west-1"]; c.Total != 2 || c.Expired != 1 {
		t.Errorf("eu-west-1 summary = %+v", c)
	}
	if f := doc.Findings[0]; f.Region != "eu-west-1" || f.Version != "4" {
		t.Errorf("worst finding = %+v, want the published version 4 in eu-west-1", f)
	}
}

func TestScanAllRegionsTable(t *testing.T) {
	fake := newFakeAWS()
	fake.install(t)
	out, _, err := runCLISplit(t, "scan", "--all-regions", "--now", "2025-07-03")
	if err != nil {
		t.Fatal(err)
	}
	if fake.allVersions[0] {
		t.Error("default --versions latest must not request all versions")
	}
	for _, want := range []string{"REGION", "VERSION", "eu-west-1: 2 function(s), 1 expired", "us-east-1: 1 function(s), 0 expired, 1 within 90d"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q\n%s", want, out)
		}
	}
}

func TestScanRegionFailureAborts(t *testing.T) {
	fake := newFakeAWS()
	fake.failRegion = "us-east-1"
	fake.install(t)
	_, _, err := runCLISplit(t, "scan", "--all-regions")
	if err == nil || !strings.Contains(err.Error(), "us-east-1") {
		t.Errorf("err = %v, want a failure naming us-east-1", err)
	}
}

func TestScanRegionFlagsValidation(t *testing.T) {
	newFakeAWS().install(t)
	for _, args := range [][]string{
		{"--region", "eu-west-1", "--regions", "us-east-1"},
		{"--regions", "us-east-1", "--all-regions"},
		{"--region", "eu-west-1", "--all-regions"},
		{"--versions", "some"},
	} {
		if _, _, err := runCLISplit(t, append([]string{"scan"}, args...)...); err == nil {
			t.Errorf("%v: expected an error", args)
		}
	}
}
