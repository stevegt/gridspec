// Package diag defines stable, source-addressed diagnostics emitted by specdot.
package diag

import (
	"fmt"
	"sort"
	"strings"
)

// Severity is ordered from least to most severe.
type Severity string

const (
	SeverityNote    Severity = "note"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// Location identifies a position in a source file. Lines and columns are
// one-based. A zero line means the diagnostic applies to the whole file.
type Location struct {
	File   string `json:"file" yaml:"file"`
	Line   int    `json:"line,omitempty" yaml:"line,omitempty"`
	Column int    `json:"column,omitempty" yaml:"column,omitempty"`
}

func (l Location) String() string {
	if l.Line == 0 {
		return l.File
	}
	if l.Column == 0 {
		return fmt.Sprintf("%s:%d", l.File, l.Line)
	}
	return fmt.Sprintf("%s:%d:%d", l.File, l.Line, l.Column)
}

// Diagnostic is one actionable finding. Rule identifiers are stable API.
type Diagnostic struct {
	Rule      string            `json:"rule" yaml:"rule"`
	Severity  Severity          `json:"severity" yaml:"severity"`
	Message   string            `json:"message" yaml:"message"`
	Location  Location          `json:"location" yaml:"location"`
	Related   []RelatedLocation `json:"related,omitempty" yaml:"related,omitempty"`
	Help      string            `json:"help,omitempty" yaml:"help,omitempty"`
	Waivable  bool              `json:"waivable" yaml:"waivable"`
	ElementID string            `json:"element_id,omitempty" yaml:"element_id,omitempty"`
}

// RelatedLocation adds supporting source evidence.
type RelatedLocation struct {
	Location Location `json:"location" yaml:"location"`
	Message  string   `json:"message,omitempty" yaml:"message,omitempty"`
}

func (d Diagnostic) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s[%s]: %s", d.Location, d.Severity, d.Rule, d.Message)
	if d.Help != "" {
		fmt.Fprintf(&b, "\n  help: %s", d.Help)
	}
	for _, rel := range d.Related {
		fmt.Fprintf(&b, "\n  related: %s", rel.Location)
		if rel.Message != "" {
			fmt.Fprintf(&b, ": %s", rel.Message)
		}
	}
	return b.String()
}

// Sort makes output deterministic across filesystems and Go versions.
func Sort(ds []Diagnostic) {
	rank := func(s Severity) int {
		switch s {
		case SeverityError:
			return 0
		case SeverityWarning:
			return 1
		default:
			return 2
		}
	}
	sort.SliceStable(ds, func(i, j int) bool {
		a, b := ds[i], ds[j]
		if a.Location.File != b.Location.File {
			return a.Location.File < b.Location.File
		}
		if a.Location.Line != b.Location.Line {
			return a.Location.Line < b.Location.Line
		}
		if a.Location.Column != b.Location.Column {
			return a.Location.Column < b.Location.Column
		}
		if rank(a.Severity) != rank(b.Severity) {
			return rank(a.Severity) < rank(b.Severity)
		}
		if a.Rule != b.Rule {
			return a.Rule < b.Rule
		}
		return a.Message < b.Message
	})
}

// HasErrors reports whether vet should return exit status 1.
func HasErrors(ds []Diagnostic) bool {
	for _, d := range ds {
		if d.Severity == SeverityError {
			return true
		}
	}
	return false
}
