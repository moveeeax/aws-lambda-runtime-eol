// Package report renders findings as a human-readable table or as JSON.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"text/tabwriter"

	"github.com/moveeeax/aws-lambda-runtime-eol/internal/eol"
)

// Meta describes the scan that produced the findings.
type Meta struct {
	Scope      string // e.g. "region eu-west-1" or "input file x.json"
	WithinDays int
	EOLSource  string // table actually used: builtin, endoflife or file:<path>
}

// Document is the top-level JSON shape emitted by --output json.
type Document struct {
	GeneratedFor string        `json:"generatedFor"` // e.g. the region, or "input file"
	WithinDays   int           `json:"withinDays"`
	EOLSource    string        `json:"eolSource"`
	Summary      Summary       `json:"summary"`
	Findings     []eol.Finding `json:"findings"`
}

// Summary is the roll-up counters. Regions is set when findings carry a region.
type Summary struct {
	Total   int                         `json:"total"`
	Expired int                         `json:"expired"`
	Warning int                         `json:"warning"`
	OK      int                         `json:"ok"`
	Regions map[string]eol.RegionCounts `json:"regions,omitempty"`
}

// JSON writes the findings as an indented JSON document.
func JSON(w io.Writer, m Meta, findings []eol.Finding) error {
	exp, warn, ok := eol.Counts(findings)
	sum := Summary{Total: len(findings), Expired: exp, Warning: warn, OK: ok}
	if byRegion := eol.CountsByRegion(findings); len(byRegion) > 0 {
		sum.Regions = byRegion
	}
	doc := Document{
		GeneratedFor: m.Scope,
		WithinDays:   m.WithinDays,
		EOLSource:    m.EOLSource,
		Summary:      sum,
		Findings:     findings,
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

// dash renders an empty cell as "-".
func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// Table writes an aligned, human-readable table.
func Table(w io.Writer, m Meta, findings []eol.Finding) error {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "FUNCTION\tREGION\tVERSION\tRUNTIME\tSTATUS\tSEVERITY\tEOL DATE\tDAYS LEFT")
	for _, f := range findings {
		days := "-"
		if f.DaysUntilEOL != nil {
			days = fmt.Sprintf("%d", *f.DaysUntilEOL)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			f.Name, dash(f.Region), dash(f.Version), f.Runtime, f.Status, f.Severity, dash(f.EOLDate), days)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	exp, warn, okc := eol.Counts(findings)
	fmt.Fprintf(w, "\n%d function(s) in %s: %d expired, %d within %dd, %d ok\n",
		len(findings), m.Scope, exp, warn, m.WithinDays, okc)

	byRegion := eol.CountsByRegion(findings)
	if len(byRegion) > 1 {
		regions := make([]string, 0, len(byRegion))
		for r := range byRegion {
			regions = append(regions, r)
		}
		sort.Strings(regions)
		for _, r := range regions {
			c := byRegion[r]
			fmt.Fprintf(w, "  %s: %d function(s), %d expired, %d within %dd, %d ok\n",
				r, c.Total, c.Expired, c.Warning, m.WithinDays, c.OK)
		}
	}
	return nil
}
