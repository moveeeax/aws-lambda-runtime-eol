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
// change on AWS's schedule; override the table with --eol-data when in doubt.
const TableAsOf = "2025-06-01"

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
	LastModified string `json:"lastModified,omitempty"`
}

// Finding is the classification result for one function.
type Finding struct {
	Name         string   `json:"name"`
	Runtime      string   `json:"runtime"`
	EOLDate      string   `json:"eolDate,omitempty"`
	DaysUntilEOL *int     `json:"daysUntilEOL,omitempty"`
	Status       Status   `json:"status"`
	Severity     Severity `json:"severity"`
}

// DefaultTable is the compiled-in map of Lambda runtime -> deprecation (phase 1)
// date, as published by AWS and reviewed on TableAsOf. It lets the tool run
// offline and deterministically; override any entry with --eol-data.
var DefaultTable = map[string]string{
	// Node.js
	"nodejs":     "2016-10-31",
	"nodejs4.3":  "2018-04-30",
	"nodejs6.10": "2019-04-30",
	"nodejs8.10": "2020-03-06",
	"nodejs10.x": "2021-07-30",
	"nodejs12.x": "2023-03-31",
	"nodejs14.x": "2023-12-04",
	"nodejs16.x": "2024-06-12",
	"nodejs18.x": "2025-09-01",
	// Python
	"python2.7": "2021-07-15",
	"python3.6": "2022-07-18",
	"python3.7": "2023-12-04",
	"python3.8": "2024-10-14",
	"python3.9": "2025-12-15",
	// Go (Amazon Linux 1 go1.x runtime)
	"go1.x": "2023-12-31",
	// Java (Amazon Linux 1)
	"java8": "2024-01-08",
	// .NET
	"dotnetcore1.0": "2019-06-27",
	"dotnetcore2.0": "2019-05-30",
	"dotnetcore2.1": "2023-01-23",
	"dotnetcore3.1": "2023-04-03",
	"dotnet5.0":     "2022-05-10",
	// Ruby
	"ruby2.5": "2021-07-30",
	"ruby2.7": "2023-12-07",
	// Legacy custom runtime on Amazon Linux 1
	"provided": "2023-12-31",
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
	fd := Finding{Name: f.Name, Runtime: f.Runtime, Status: StatusOK, Severity: SeverityNone}

	eol, known := t.dates[f.Runtime]
	if !known {
		// Unknown or current runtime: nothing to warn about.
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
// severity (worst first) and then by name, so output is stable.
func (t *Table) Classify(funcs []Function, now time.Time, within int) []Finding {
	out := make([]Finding, 0, len(funcs))
	for _, f := range funcs {
		out = append(out, t.classifyOne(f, now, within))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if ri, rj := rank(out[i].Severity), rank(out[j].Severity); ri != rj {
			return ri > rj
		}
		return out[i].Name < out[j].Name
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
