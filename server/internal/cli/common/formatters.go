// Package common provides shared CLI plumbing used across every gdhcp
// resource command: output rendering (table|json|yaml, per the --output
// persistent flag) and small flag/formatting helpers.
package common

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"text/tabwriter"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/shamalawy/gdhcp/server/internal/models"
)

// OutputTable, OutputJSON, and OutputYAML are the values accepted by the
// --output persistent flag (root.go).
const (
	OutputTable = "table"
	OutputJSON  = "json"
	OutputYAML  = "yaml"
)

// ValidOutputFormat reports whether format is one of the supported
// --output values.
func ValidOutputFormat(format string) bool {
	switch format {
	case OutputTable, OutputJSON, OutputYAML:
		return true
	default:
		return false
	}
}

// TableFunc lazily renders data as a table's headers and rows. It is only
// invoked when the active output format is "table" — JSON/YAML render data
// directly without needing a table-shaped view of it.
type TableFunc func() (headers []string, rows [][]string)

// Render writes data to w in the requested format: parsed JSON/YAML from
// data directly (preserving every field, including ones a table view
// would omit for width), or an aligned table built from tbl().
func Render(w io.Writer, format string, data any, tbl TableFunc) error {
	switch format {
	case OutputJSON:
		return PrintJSON(w, data)
	case OutputYAML:
		return PrintYAML(w, data)
	default:
		headers, rows := tbl()
		PrintTable(w, headers, rows)
		return nil
	}
}

// PrintJSON writes data as indented JSON followed by a newline.
func PrintJSON(w io.Writer, data any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(data)
}

// PrintYAML writes data as YAML.
func PrintYAML(w io.Writer, data any) error {
	enc := yaml.NewEncoder(w)
	defer enc.Close()
	return enc.Encode(data)
}

// PrintTable writes headers and rows as a tab-aligned table. An empty rows
// slice still prints the header line, so the reader can see which columns
// were requested even when nothing matched.
func PrintTable(w io.Writer, headers []string, rows [][]string) {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(headers, "\t"))
	for _, row := range rows {
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	tw.Flush()
}

// Str renders a *string for a table cell: "-" for nil/empty, the value
// otherwise.
func Str(s *string) string {
	if s == nil || *s == "" {
		return "-"
	}
	return *s
}

// Bool renders a bool for a table cell as "yes"/"no".
func Bool(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// Int64Ptr renders an *int64 for a table cell.
func Int64Ptr(i *int64) string {
	if i == nil {
		return "-"
	}
	return fmt.Sprintf("%d", *i)
}

// IntPtr renders an *int for a table cell.
func IntPtr(i *int) string {
	if i == nil {
		return "-"
	}
	return fmt.Sprintf("%d", *i)
}

// Time renders a time.Time for a table cell as RFC3339, or "-" for the
// zero value.
func Time(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format(time.RFC3339)
}

// TimePtr renders a *time.Time for a table cell.
func TimePtr(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return Time(*t)
}

// IP renders a net.IP for a table cell, "-" for nil/unset.
func IP(ip net.IP) string {
	if ip == nil {
		return "-"
	}
	return ip.String()
}

// CIDR renders a *models.IPNet for a table cell, "-" for nil/unset.
func CIDR(n *models.IPNet) string {
	if n == nil || n.IP == nil {
		return "-"
	}
	return n.String()
}

// MAC renders a *models.HardwareAddr for a table cell, "-" for nil/unset.
func MAC(h *models.HardwareAddr) string {
	if h == nil || len(*h) == 0 {
		return "-"
	}
	return h.String()
}
