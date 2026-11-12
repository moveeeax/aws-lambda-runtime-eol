// Package eol holds the runtime end-of-life table and the pure classifier that
// turns a list of Lambda functions into severity-ranked findings. Nothing in
// this package talks to AWS, so it is fully unit-testable and deterministic.
package eol

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"
)

// DateLayout is the format used everywhere for EOL and last-modified dates.
const DateLayout = "2006-01-02"

// TableAsOf documents when the embedded EOL table was last reviewed. Runtimes
// change on AWS's schedule; use --eol-source endoflife or a file when in doubt.
const TableAsOf = "2026-11-02"

// Status is the classification of a single function's runtime.
type Status string

const (
	StatusOK      Status = "OK"      // runtime not deprecated within the window
	StatusWarning Status = "WARNING" // deprecated within --within days
	StatusExpired Status = "EXPIRED" // already past its EOL date
)

// Severity ranks findings so a --fail-on gate can compare them.
type Severity string

const (
	SeverityNone   Severity = "none"
	SeverityMedium Severity = "medium"
	SeverityHigh   Severity = "high"
)

// rank gives an orderable weight to a severity (higher = worse).
func rank(s Severity) int {
	switch s {
	case SeverityHigh:
		return 2
	case SeverityMedium:
		return 1
	default:
		return 0
	}
}

// AtLeast reports whether s is as severe as, or worse than, threshold.
func (s Severity) AtLeast(threshold Severity) bool {
	return rank(s) >= rank(threshold)
}

// ParseSeverity validates a user-supplied --fail-on value.
func ParseSeverity(s string) (Severity, error) {
	switch Severity(s) {
	case SeverityHigh:
		return SeverityHigh, nil
	case SeverityMedium:
		return SeverityMedium, nil
	default:
		return "", fmt.Errorf("invalid severity %q (want high or medium)", s)
	}
}

// Function is a Lambda function as far as this tool cares about it.
type Function struct {
	Name         string `json:"name"`
	Runtime      string `json:"runtime"`
	Region       string `json:"region,omitempty"`
	Version      string `json:"version,omitempty"` // "$LATEST" or a published version number
	LastModified string `json:"lastModified,omitempty"`
}

// Finding is the classification result for one function.
type Finding struct {
	Name         string   `json:"name"`
	Runtime      string   `json:"runtime"`
	Region       string   `json:"region,omitempty"`
	Version      string   `json:"version,omitempty"`
	EOLDate      string   `json:"eolDate,omitempty"`
	DaysUntilEOL *int     `json:"daysUntilEOL,omitempty"`
	Status       Status   `json:"status"`
	Severity     Severity `json:"severity"`
}

// DefaultTable is the compiled-in map of Lambda runtime -> deprecation date, as
// published in the "Supported runtimes" and "Deprecated runtimes" tables of
// https://docs.aws.amazon.com/lambda/latest/dg/lambda-runtimes.html and reviewed
// on TableAsOf. Supported runtimes are listed with their scheduled deprecation
// date so they move to WARNING ahead of time; runtimes AWS lists as "Not
// scheduled" (and anything else missing here) classify as OK. The table lets the
// tool run offline and deterministically; override it with --eol-source.
var DefaultTable = map[string]string{
	// Node.js
	"nodejs":         "2016-08-30",
	"nodejs4.3":      "2020-03-05",
	"nodejs4.3-edge": "2020-03-05",
	"nodejs6.10":     "2019-08-12",
	"nodejs8.10":     "2020-03-06",
	"nodejs10.x":     "2021-07-30",
	"nodejs12.x":     "2023-03-31",
	"nodejs14.x":     "2023-12-04",
	"nodejs16.x":     "2024-06-12",
	"nodejs18.x":     "2025-09-01",
	"nodejs20.x":     "2026-04-30",
	"nodejs22.x":     "2027-04-30",
	"nodejs24.x":     "2028-04-30",
	// Python
	"python2.7":  "2021-07-15",
	"python3.6":  "2022-07-18",
	"python3.7":  "2023-12-04",
	"python3.8":  "2024-10-14",
	"python3.9":  "2025-12-15",
	"python3.10": "2026-10-31",
	"python3.11": "2027-06-30",
	"python3.12": "2028-10-31",
	"python3.13": "2029-06-30",
	"python3.14": "2029-06-30",
	// Go (Amazon Linux 1 go1.x runtime)
	"go1.x": "2024-01-08",
	// Java
	"java8":         "2024-01-08",
	"java8.al2":     "2027-06-30",
	"java11":        "2027-06-30",
	"java17":        "2027-06-30",
	"java8.al2023":  "2029-06-30",
	"java11.al2023": "2029-06-30",
	"java17.al2023": "2029-06-30",
	"java21":        "2029-06-30",
	"java25":        "2029-06-30",
	// .NET
	"dotnetcore1.0": "2019-06-27",
	"dotnetcore2.0": "2019-05-30",
	"dotnetcore2.1": "2022-01-05",
	"dotnetcore3.1": "2023-04-03",
	"dotnet5.0":     "2022-05-10",
	"dotnet6":       "2024-12-20",
	"dotnet7":       "2024-05-14",
	"dotnet8":       "2026-11-10",
	"dotnet9":       "2026-11-10",
	"dotnet10":      "2028-11-14",
	// Ruby
	"ruby2.5": "2021-07-30",
	"ruby2.7": "2023-12-07",
	"ruby3.2": "2026-03-31",
	"ruby3.3": "2027-03-31",
	"ruby3.4": "2028-03-31",
	"ruby4.0": "2029-03-31",
	// OS-only runtimes
	"provided":        "2024-01-08",
	"provided.al2":    "2026-07-31",
	"provided.al2023": "2029-06-30",
}

// Table is a runtime -> EOL date lookup with the classification logic attached.
type Table struct {
	dates map[string]time.Time
}

// NewDefaultTable returns the compiled-in table.
func NewDefaultTable() (*Table, error) {
	return newTable(DefaultTable)
}

// LoadTable reads a JSON object of {"runtime": "YYYY-MM-DD"} from path and uses
// it in place of the embedded table.
func LoadTable(path string) (*Table, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse eol data %s: %w", path, err)
	}
	return newTable(m)
}

func newTable(m map[string]string) (*Table, error) {
	dates := make(map[string]time.Time, len(m))
	for runtime, ds := range m {
		d, err := time.Parse(DateLayout, ds)
		if err != nil {
			return nil, fmt.Errorf("bad eol date for %q: %w", runtime, err)
		}
		dates[runtime] = d.UTC()
	}
	return &Table{dates: dates}, nil
}

// classifyOne classifies a single function against the table.
func (t *Table) classifyOne(f Function, now time.Time, within int) Finding {
	now = now.UTC().Truncate(24 * time.Hour)
	fd := Finding{Name: f.Name, Runtime: f.Runtime, Region: f.Region, Version: f.Version, Status: StatusOK, Severity: SeverityNone}

	eol, known := t.dates[f.Runtime]
	if !known {
		// Not in the table: either a runtime AWS has not scheduled for
		// deprecation yet, or one this table does not know. Report OK.
		return fd
	}
	fd.EOLDate = eol.Format(DateLayout)
	days := int(eol.Sub(now).Hours() / 24)
	fd.DaysUntilEOL = &days

	switch {
	case now.After(eol):
		fd.Status = StatusExpired
		fd.Severity = SeverityHigh
	case days <= within:
		fd.Status = StatusWarning
		fd.Severity = SeverityMedium
	}
	return fd
}

// Classify runs every function through the table and returns findings sorted by
// severity (worst first), then by name, region and version, so output is
// stable.
func (t *Table) Classify(funcs []Function, now time.Time, within int) []Finding {
	out := make([]Finding, 0, len(funcs))
	for _, f := range funcs {
		out = append(out, t.classifyOne(f, now, within))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if ri, rj := rank(out[i].Severity), rank(out[j].Severity); ri != rj {
			return ri > rj
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		if out[i].Region != out[j].Region {
			return out[i].Region < out[j].Region
		}
		return out[i].Version < out[j].Version
	})
	return out
}

// Worst returns the highest severity present in the findings.
func Worst(findings []Finding) Severity {
	worst := SeverityNone
	for _, f := range findings {
		if rank(f.Severity) > rank(worst) {
			worst = f.Severity
		}
	}
	return worst
}

// RegionCounts is the per-region roll-up used in multi-region summaries.
type RegionCounts struct {
	Total   int `json:"total"`
	Expired int `json:"expired"`
	Warning int `json:"warning"`
	OK      int `json:"ok"`
}

// CountsByRegion groups findings by region. Findings without a region (for
// example from an --input file that does not set one) are not counted.
func CountsByRegion(findings []Finding) map[string]RegionCounts {
	out := map[string]RegionCounts{}
	for _, f := range findings {
		if f.Region == "" {
			continue
		}
		c := out[f.Region]
		c.Total++
		switch f.Status {
		case StatusExpired:
			c.Expired++
		case StatusWarning:
			c.Warning++
		default:
			c.OK++
		}
		out[f.Region] = c
	}
	return out
}

// Counts summarises findings by status for a one-line footer.
func Counts(findings []Finding) (expired, warning, ok int) {
	for _, f := range findings {
		switch f.Status {
		case StatusExpired:
			expired++
		case StatusWarning:
			warning++
		default:
			ok++
		}
	}
	return
}
