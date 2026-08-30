package common

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/shamalawy/gdhcp/server/internal/models"
)

func TestPrintTable(t *testing.T) {
	var buf bytes.Buffer
	PrintTable(&buf, []string{"ID", "NAME"}, [][]string{
		{"1", "alpha"},
		{"2", "beta-longer-name"},
	})
	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (header + 2 rows), got %d: %q", len(lines), out)
	}
	if !strings.HasPrefix(lines[0], "ID") || !strings.Contains(lines[0], "NAME") {
		t.Errorf("header line malformed: %q", lines[0])
	}
	// Columns must align: the NAME column starts at the same offset on
	// every line.
	idx1 := strings.Index(lines[1], "alpha")
	idx2 := strings.Index(lines[2], "beta-longer-name")
	if idx1 != idx2 {
		t.Errorf("columns not aligned: NAME starts at %d on row1, %d on row2", idx1, idx2)
	}
}

func TestPrintTableEmptyRows(t *testing.T) {
	var buf bytes.Buffer
	PrintTable(&buf, []string{"ID"}, nil)
	if strings.TrimSpace(buf.String()) != "ID" {
		t.Errorf("expected just the header line, got %q", buf.String())
	}
}

func TestRenderJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, OutputJSON, map[string]string{"name": "x"}, nil); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(buf.String(), `"name": "x"`) {
		t.Errorf("expected JSON output, got %q", buf.String())
	}
}

func TestRenderYAML(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, OutputYAML, map[string]string{"name": "x"}, nil); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(buf.String(), "name: x") {
		t.Errorf("expected YAML output, got %q", buf.String())
	}
}

func TestRenderTableUsesCallback(t *testing.T) {
	var buf bytes.Buffer
	called := false
	tbl := func() ([]string, [][]string) {
		called = true
		return []string{"A"}, [][]string{{"1"}}
	}
	if err := Render(&buf, OutputTable, nil, tbl); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !called {
		t.Error("expected table callback to be invoked for table output")
	}
	if !strings.Contains(buf.String(), "A") {
		t.Errorf("expected table header in output, got %q", buf.String())
	}
}

func TestValidOutputFormat(t *testing.T) {
	for _, f := range []string{OutputTable, OutputJSON, OutputYAML} {
		if !ValidOutputFormat(f) {
			t.Errorf("expected %q to be valid", f)
		}
	}
	if ValidOutputFormat("xml") {
		t.Error("expected xml to be invalid")
	}
}

func TestScalarFormatters(t *testing.T) {
	if got := Str(nil); got != "-" {
		t.Errorf("Str(nil) = %q, want -", got)
	}
	s := "hi"
	if got := Str(&s); got != "hi" {
		t.Errorf("Str(&s) = %q, want hi", got)
	}
	if got := Bool(true); got != "yes" {
		t.Errorf("Bool(true) = %q, want yes", got)
	}
	if got := Bool(false); got != "no" {
		t.Errorf("Bool(false) = %q, want no", got)
	}
	if got := Time(time.Time{}); got != "-" {
		t.Errorf("Time(zero) = %q, want -", got)
	}
	if got := TimePtr(nil); got != "-" {
		t.Errorf("TimePtr(nil) = %q, want -", got)
	}

	var n *models.IPNet
	if got := CIDR(n); got != "-" {
		t.Errorf("CIDR(nil) = %q, want -", got)
	}
	parsed, err := models.ParseIPNet("10.0.0.0/24")
	if err != nil {
		t.Fatalf("ParseIPNet: %v", err)
	}
	if got := CIDR(parsed); got != "10.0.0.0/24" {
		t.Errorf("CIDR(parsed) = %q, want 10.0.0.0/24", got)
	}

	var h *models.HardwareAddr
	if got := MAC(h); got != "-" {
		t.Errorf("MAC(nil) = %q, want -", got)
	}
}
