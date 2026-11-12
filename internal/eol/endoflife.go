package eol

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// EndOfLifeURL is the endoflife.date v1 API endpoint for AWS Lambda runtimes.
const EndOfLifeURL = "https://endoflife.date/api/v1/products/aws-lambda/"

// EndOfLifeTimeout bounds the whole endoflife.date request so a slow or
// unreachable API cannot hang a CI job.
const EndOfLifeTimeout = 10 * time.Second

// maxEndOfLifeBody caps how much of the response is read (the real document is
// a few tens of KB).
const maxEndOfLifeBody = 4 << 20

// endOfLifeDoc is the subset of the endoflife.date v1 product document we use.
// Each release name is a Lambda runtime identifier (e.g. "nodejs18.x").
// eoasFrom ("Standard Support" end) is the AWS deprecation date, the same date
// the built-in table carries; eolFrom is the later "Deprecated Support" end,
// when AWS blocks function updates, and is not used for classification.
type endOfLifeDoc struct {
	Result struct {
		Releases []struct {
			Name     string  `json:"name"`
			EOASFrom *string `json:"eoasFrom"`
		} `json:"releases"`
	} `json:"result"`
}

// ParseEndOfLife turns an endoflife.date v1 aws-lambda document into a
// {runtime: "YYYY-MM-DD"} map keyed by runtime identifier. Releases without a
// deprecation date are left out, so they classify as OK like any runtime the
// table does not know.
func ParseEndOfLife(r io.Reader) (map[string]string, error) {
	var doc endOfLifeDoc
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("parse endoflife.date response: %w", err)
	}
	m := make(map[string]string, len(doc.Result.Releases))
	for _, rel := range doc.Result.Releases {
		if rel.Name == "" || rel.EOASFrom == nil || *rel.EOASFrom == "" {
			continue
		}
		if _, err := time.Parse(DateLayout, *rel.EOASFrom); err != nil {
			return nil, fmt.Errorf("endoflife.date: bad eoasFrom for %q: %w", rel.Name, err)
		}
		m[rel.Name] = *rel.EOASFrom
	}
	if len(m) == 0 {
		return nil, fmt.Errorf("endoflife.date: response has no runtime deprecation dates")
	}
	return m, nil
}

// FetchEndOfLife downloads the endoflife.date document at url and returns a
// table built from it. Runtimes missing from the response keep their built-in
// date, so a release endoflife.date has not listed yet does not silently turn
// into OK.
func FetchEndOfLife(ctx context.Context, client *http.Client, url string) (*Table, error) {
	ctx, cancel := context.WithTimeout(ctx, EndOfLifeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch endoflife.date: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch endoflife.date: HTTP %d", resp.StatusCode)
	}
	live, err := ParseEndOfLife(io.LimitReader(resp.Body, maxEndOfLifeBody))
	if err != nil {
		return nil, err
	}
	merged := make(map[string]string, len(DefaultTable)+len(live))
	for rt, d := range DefaultTable {
		merged[rt] = d
	}
	for rt, d := range live {
		merged[rt] = d
	}
	return newTable(merged)
}
