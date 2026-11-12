package eol

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestParseEndOfLifeFixture(t *testing.T) {
	// Trimmed copy of the real https://endoflife.date/api/v1/products/aws-lambda/ response.
	f, err := os.Open("testdata/endoflife-aws-lambda.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	m, err := ParseEndOfLife(f)
	if err != nil {
		t.Fatalf("ParseEndOfLife: %v", err)
	}
	// eoasFrom is the deprecation date and must match the built-in table.
	for _, rt := range []string{"nodejs18.x", "python3.12", "dotnet8", "go1.x"} {
		if m[rt] != DefaultTable[rt] {
			t.Errorf("%s = %q, want %q (eoasFrom, as in the built-in table)", rt, m[rt], DefaultTable[rt])
		}
	}
}

func TestParseEndOfLifeSkipsMissingDates(t *testing.T) {
	body := `{"result":{"releases":[
	  {"name":"nodejs18.x","eoasFrom":"2025-09-01"},
	  {"name":"python3.99","eoasFrom":null}
	]}}`
	m, err := ParseEndOfLife(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m["python3.99"]; ok {
		t.Error("release without eoasFrom should be left out")
	}
	if len(m) != 1 {
		t.Errorf("got %v, want only nodejs18.x", m)
	}
}

func TestParseEndOfLifeRejectsEmptyAndBadDates(t *testing.T) {
	for name, body := range map[string]string{
		"empty":    `{"result":{"releases":[]}}`,
		"bad date": `{"result":{"releases":[{"name":"nodejs18.x","eoasFrom":"Sep 2025"}]}}`,
		"not json": `<html>`,
	} {
		if _, err := ParseEndOfLife(strings.NewReader(body)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestFetchEndOfLifeOverridesAndKeepsBuiltin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Live data moves nodejs18.x and does not list python3.8 at all.
		w.Write([]byte(`{"result":{"releases":[{"name":"nodejs18.x","eoasFrom":"2026-01-31"}]}}`))
	}))
	defer srv.Close()

	tbl, err := FetchEndOfLife(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("FetchEndOfLife: %v", err)
	}
	now := date(t, "2025-07-03")
	got := tbl.Classify([]Function{
		{Name: "a", Runtime: "nodejs18.x"},
		{Name: "b", Runtime: "python3.8"},
	}, now, 90)
	byName := map[string]Finding{got[0].Name: got[0], got[1].Name: got[1]}
	if byName["a"].EOLDate != "2026-01-31" || byName["a"].Status != StatusOK {
		t.Errorf("nodejs18.x = %s/%s, want live date 2026-01-31 and OK", byName["a"].EOLDate, byName["a"].Status)
	}
	if byName["b"].EOLDate != "2024-10-14" || byName["b"].Status != StatusExpired {
		t.Errorf("python3.8 = %s/%s, want built-in 2024-10-14 and EXPIRED", byName["b"].EOLDate, byName["b"].Status)
	}
}

func TestFetchEndOfLifeHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	if _, err := FetchEndOfLife(context.Background(), srv.Client(), srv.URL); err == nil {
		t.Error("expected an error on HTTP 503")
	}
}
