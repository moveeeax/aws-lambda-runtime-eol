package eol

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func mustTable(t *testing.T) *Table {
	t.Helper()
	tbl, err := NewDefaultTable()
	if err != nil {
		t.Fatalf("NewDefaultTable: %v", err)
	}
	return tbl
}

func date(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse(DateLayout, s)
	if err != nil {
		t.Fatalf("parse date %s: %v", s, err)
	}
	return d
}

func TestClassifyExpired(t *testing.T) {
	tbl := mustTable(t)
	now := date(t, "2026-01-01")
	got := tbl.Classify([]Function{{Name: "legacy", Runtime: "python3.8"}}, now, 90)
	if len(got) != 1 {
		t.Fatalf("want 1 finding, got %d", len(got))
	}
	f := got[0]
	if f.Status != StatusExpired {
		t.Errorf("status = %s, want EXPIRED", f.Status)
	}
	if f.Severity != SeverityHigh {
		t.Errorf("severity = %s, want high", f.Severity)
	}
	if f.EOLDate != "2024-10-14" {
		t.Errorf("eolDate = %s, want 2024-10-14", f.EOLDate)
	}
	if f.DaysUntilEOL == nil || *f.DaysUntilEOL >= 0 {
		t.Errorf("expired function should report negative daysUntilEOL, got %v", f.DaysUntilEOL)
	}
}

func TestClassifyWarningAndOK(t *testing.T) {
	tbl := mustTable(t)
	// nodejs18.x EOL is 2025-09-01.
	// 60 days before EOL -> within a 90-day window (WARNING), outside a 30-day window (OK).
	now := date(t, "2025-07-03")

	warn := tbl.Classify([]Function{{Name: "api", Runtime: "nodejs18.x"}}, now, 90)[0]
	if warn.Status != StatusWarning || warn.Severity != SeverityMedium {
		t.Errorf("within 90d: got %s/%s, want WARNING/medium", warn.Status, warn.Severity)
	}

	ok := tbl.Classify([]Function{{Name: "api", Runtime: "nodejs18.x"}}, now, 30)[0]
	if ok.Status != StatusOK || ok.Severity != SeverityNone {
		t.Errorf("within 30d: got %s/%s, want OK/none", ok.Status, ok.Severity)
	}
}

func TestClassifyUnknownRuntimeIsOK(t *testing.T) {
	tbl := mustTable(t)
	now := date(t, "2026-01-01")
	// nodejs26.x has no deprecation date scheduled by AWS, so it is not in the table.
	f := tbl.Classify([]Function{{Name: "fresh", Runtime: "nodejs26.x"}}, now, 90)[0]
	if f.Status != StatusOK || f.Severity != SeverityNone {
		t.Errorf("unknown runtime: got %s/%s, want OK/none", f.Status, f.Severity)
	}
	if f.EOLDate != "" {
		t.Errorf("unknown runtime should have no EOL date, got %q", f.EOLDate)
	}
}

func TestClassifyBoundaryIsWarningNotExpired(t *testing.T) {
	tbl := mustTable(t)
	// Exactly on the EOL date is not yet past it -> WARNING (0 days left), not EXPIRED.
	now := date(t, "2024-10-14") // python3.8 EOL
	f := tbl.Classify([]Function{{Name: "edge", Runtime: "python3.8"}}, now, 90)[0]
	if f.Status != StatusWarning {
		t.Errorf("on-EOL-date status = %s, want WARNING", f.Status)
	}
	if f.DaysUntilEOL == nil || *f.DaysUntilEOL != 0 {
		t.Errorf("on-EOL-date daysUntilEOL = %v, want 0", f.DaysUntilEOL)
	}
}

func TestClassifySortsWorstFirst(t *testing.T) {
	tbl := mustTable(t)
	now := date(t, "2025-07-03")
	funcs := []Function{
		{Name: "zeta-ok", Runtime: "nodejs20.x"},
		{Name: "alpha-warn", Runtime: "nodejs18.x"},
		{Name: "beta-expired", Runtime: "python3.8"},
	}
	got := tbl.Classify(funcs, now, 90)
	order := []string{got[0].Name, got[1].Name, got[2].Name}
	want := []string{"beta-expired", "alpha-warn", "zeta-ok"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("sort order = %v, want %v", order, want)
		}
	}
}

func TestSeverityAtLeast(t *testing.T) {
	if !SeverityHigh.AtLeast(SeverityMedium) {
		t.Error("high should satisfy a medium gate")
	}
	if SeverityMedium.AtLeast(SeverityHigh) {
		t.Error("medium should not satisfy a high gate")
	}
	if SeverityNone.AtLeast(SeverityMedium) {
		t.Error("none should not satisfy a medium gate")
	}
}

func TestWorstAndCounts(t *testing.T) {
	tbl := mustTable(t)
	now := date(t, "2025-07-03")
	funcs := []Function{
		{Name: "a", Runtime: "python3.8"},  // expired/high
		{Name: "b", Runtime: "nodejs18.x"}, // warning/medium
		{Name: "c", Runtime: "nodejs20.x"}, // ok/none
	}
	got := tbl.Classify(funcs, now, 90)
	if Worst(got) != SeverityHigh {
		t.Errorf("Worst = %s, want high", Worst(got))
	}
	exp, warn, ok := Counts(got)
	if exp != 1 || warn != 1 || ok != 1 {
		t.Errorf("Counts = %d/%d/%d, want 1/1/1", exp, warn, ok)
	}
}

func TestLoadTableOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "eol.json")
	if err := os.WriteFile(path, []byte(`{"customruntime": "2020-01-01"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	tbl, err := LoadTable(path)
	if err != nil {
		t.Fatalf("LoadTable: %v", err)
	}
	now := date(t, "2026-01-01")
	f := tbl.Classify([]Function{{Name: "x", Runtime: "customruntime"}}, now, 90)[0]
	if f.Status != StatusExpired {
		t.Errorf("override runtime status = %s, want EXPIRED", f.Status)
	}
	// A runtime from the default table must no longer be known.
	f2 := tbl.Classify([]Function{{Name: "y", Runtime: "python3.8"}}, now, 90)[0]
	if f2.Status != StatusOK {
		t.Errorf("override should replace default table; python3.8 status = %s, want OK", f2.Status)
	}
}

func TestLoadTableBadDate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte(`{"r": "not-a-date"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTable(path); err == nil {
		t.Error("expected error for malformed EOL date")
	}
}

func TestParseSeverity(t *testing.T) {
	if _, err := ParseSeverity("high"); err != nil {
		t.Errorf("high should parse: %v", err)
	}
	if _, err := ParseSeverity("low"); err == nil {
		t.Error("low should be rejected")
	}
}

// Runtimes AWS deprecated after the table's previous review (2025-06-01) must
// classify as EXPIRED, not fall through to OK as unknown runtimes.
func TestDefaultTableCoversRecentDeprecations(t *testing.T) {
	tbl := mustTable(t)
	now := date(t, "2026-11-02")
	want := map[string]string{
		"nodejs20.x":   "2026-04-30",
		"ruby3.2":      "2026-03-31",
		"provided.al2": "2026-07-31",
		"python3.10":   "2026-10-31",
		"python3.9":    "2025-12-15",
		"dotnet6":      "2024-12-20",
		"dotnet7":      "2024-05-14",
	}
	for runtime, eolDate := range want {
		f := tbl.Classify([]Function{{Name: "f", Runtime: runtime}}, now, 90)[0]
		if f.Status != StatusExpired || f.EOLDate != eolDate {
			t.Errorf("%s: got %s/%q, want EXPIRED/%q", runtime, f.Status, f.EOLDate, eolDate)
		}
	}
	// dotnet8 is deprecated on 2026-11-10: inside a 90-day window, not yet past it.
	f := tbl.Classify([]Function{{Name: "f", Runtime: "dotnet8"}}, now, 90)[0]
	if f.Status != StatusWarning {
		t.Errorf("dotnet8: got %s, want WARNING", f.Status)
	}
}

func TestDefaultTableParses(t *testing.T) {
	if _, err := NewDefaultTable(); err != nil {
		t.Fatalf("embedded table has a bad date: %v", err)
	}
}
