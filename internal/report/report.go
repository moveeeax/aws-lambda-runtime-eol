// Package report renders findings as a human-readable table or as JSON.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/moveeeax/aws-lambda-runtime-eol/internal/eol"
)

// Document is the top-level JSON shape emitted by --output json.
type Document struct {
	GeneratedFor string        `json:"generatedFor"` // e.g. the region, or "input file"
	WithinDays   int           `json:"withinDays"`
	Summary      Summary       `json:"summary"`
	Findings     []eol.Finding `json:"findings"`
}

// Summary is the roll-up counters.
type Summary struct {
	Total   int `json:"total"`
	Expired int `json:"expired"`
	Warning int `json:"warning"`
	OK      int `json:"ok"`
}

// JSON writes the findings as an indented JSON document.
func JSON(w io.Writer, scope string, within int, findings []eol.Finding) error {
	exp, warn, ok := eol.Counts(findings)
	doc := Document{
		GeneratedFor: scope,
		WithinDays:   within,
		Summary:      Summary{Total: len(findings), Expired: exp, Warning: warn, OK: ok},
		Findings:     findings,
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

// Table writes an aligned, human-readable table.
func Table(w io.Writer, scope string, within int, findings []eol.Finding) error {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "FUNCTION\tRUNTIME\tSTATUS\tSEVERITY\tEOL DATE\tDAYS LEFT")
	for _, f := range findings {
		days := "-"
		if f.DaysUntilEOL != nil {
			days = fmt.Sprintf("%d", *f.DaysUntilEOL)
		}
		eolDate := f.EOLDate
		if eolDate == "" {
			eolDate = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			f.Name, f.Runtime, f.Status, f.Severity, eolDate, days)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	exp, warn, okc := eol.Counts(findings)
	fmt.Fprintf(w, "\n%d function(s) in %s: %d expired, %d within %dd, %d ok\n",
		len(findings), scope, exp, warn, within, okc)
	return nil
}
