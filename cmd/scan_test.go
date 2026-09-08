package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moveeeax/aws-lambda-runtime-eol/internal/report"
)

// writeFixture drops a small offline function set on disk.
func writeFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "functions.json")
	body := `[
	  {"name": "billing-cron", "runtime": "python3.8"},
	  {"name": "edge-router",  "runtime": "nodejs18.x"},
	  {"name": "image-resize", "runtime": "nodejs20.x"}
	]`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// runCLI executes the root command with args and captures stdout + exit code.
func runCLI(t *testing.T, args ...string) (string, int) {
	t.Helper()
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	err := root.Execute()
	code := 0
	if err != nil {
		if ec, ok := err.(*exitError); ok {
			code = ec.code
		} else {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	return buf.String(), code
}

func TestScanJSONOffline(t *testing.T) {
	fx := writeFixture(t)
	out, code := runCLI(t, "scan", "--input", fx, "--output", "json", "--now", "2025-07-03", "--within", "90")
	if code != 0 {
		t.Fatalf("exit = %d without --fail-on, want 0", code)
	}
	var doc report.Document
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if doc.Summary.Total != 3 {
		t.Errorf("total = %d, want 3", doc.Summary.Total)
	}
	if doc.Summary.Expired != 1 || doc.Summary.Warning != 1 || doc.Summary.OK != 1 {
		t.Errorf("summary = %+v, want 1/1/1", doc.Summary)
	}
	// Worst finding must sort first.
	if doc.Findings[0].Name != "billing-cron" {
		t.Errorf("first finding = %s, want billing-cron", doc.Findings[0].Name)
	}
}

func TestScanFailOnHigh(t *testing.T) {
	fx := writeFixture(t)
	_, code := runCLI(t, "scan", "--input", fx, "--now", "2025-07-03", "--fail-on", "high")
	if code != 2 {
		t.Errorf("exit = %d with an expired function and --fail-on high, want 2", code)
	}
}

func TestScanFailOnHighClean(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clean.json")
	// Only a current runtime -> no findings meet a high gate.
	if err := os.WriteFile(path, []byte(`[{"name":"ok","runtime":"nodejs20.x"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, code := runCLI(t, "scan", "--input", path, "--now", "2025-07-03", "--fail-on", "high")
	if code != 0 {
		t.Errorf("exit = %d with no expired functions, want 0", code)
	}
}

func TestScanFailOnMediumCatchesWarning(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "warn.json")
	if err := os.WriteFile(path, []byte(`[{"name":"api","runtime":"nodejs18.x"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	// 60 days before nodejs18.x EOL -> WARNING/medium.
	_, code := runCLI(t, "scan", "--input", path, "--now", "2025-07-03", "--within", "90", "--fail-on", "medium")
	if code != 2 {
		t.Errorf("exit = %d for a warning with --fail-on medium, want 2", code)
	}
}

func TestScanTableOutput(t *testing.T) {
	fx := writeFixture(t)
	out, _ := runCLI(t, "scan", "--input", fx, "--now", "2025-07-03")
	for _, want := range []string{"FUNCTION", "billing-cron", "EXPIRED", "expired"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q\n%s", want, out)
		}
	}
}

func TestScanRejectsBadOutput(t *testing.T) {
	fx := writeFixture(t)
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"scan", "--input", fx, "--output", "yaml"})
	if err := root.Execute(); err == nil {
		t.Error("expected error for --output yaml")
	}
}

func TestReadInputWrappedObject(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wrapped.json")
	if err := os.WriteFile(path, []byte(`{"functions":[{"name":"a","runtime":"go1.x"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	funcs, err := readInputFile(path)
	if err != nil {
		t.Fatalf("readInputFile: %v", err)
	}
	if len(funcs) != 1 || funcs[0].Runtime != "go1.x" {
		t.Errorf("got %+v, want one go1.x function", funcs)
	}
}
