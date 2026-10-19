package conform

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/promisegrid/gridspec/internal/diag"
)

func WriteReport(w io.Writer, format string, report Report) error {
	switch format {
	case "", "text":
		for _, d := range report.Diagnostics {
			if _, err := fmt.Fprintln(w, d.String()); err != nil {
				return err
			}
		}
		for _, class := range []string{"static", "command", "go_test", "trace"} {
			coverage := report.Coverage[class]
			if _, err := fmt.Fprintf(w, "%s: %d/%d (%.2f%%, floor %.2f%%)\n", class, coverage.Covered, coverage.Eligible, coverage.Percent, coverage.Floor); err != nil {
				return err
			}
		}
		status := "clean"
		if diag.HasErrors(report.Diagnostics) {
			status = "failed"
		}
		_, err := fmt.Fprintf(w, "%s: conformance %s, %d exclusion(s) [%s]\n", report.Implementation, status, len(report.Exclusions), report.SpecDigest)
		return err
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	default:
		return fmt.Errorf("unknown conformance output format %q", format)
	}
}
