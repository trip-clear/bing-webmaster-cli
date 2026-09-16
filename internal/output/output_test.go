package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestFormatters(t *testing.T) {
	for _, tc := range []struct{ got, want string }{
		{Int(1234), "1234"},
		{Pct(0.0731), "7.31%"},
		{Pos(4.26), "4.3"},
		{Pos(4.24), "4.2"},
		{DeltaInt(120, 100), "+20"},
		{DeltaInt(80, 100), "-20"},
		{DeltaInt(100, 100), "0"}, // no "+0" and no "-0"
		{DeltaPct(0.08, 0.05), "+3.00pt"},
		// Position: lower is better, so an improvement reads as a positive number.
		{DeltaPos(8, 12), "+4.0"},
		{DeltaPos(12, 8), "-4.0"},
		{Growth(138, 100), "+38.0%"},
		{Growth(50, 0), "new"},
		{Growth(0, 0), "-"},
	} {
		if tc.got != tc.want {
			t.Errorf("got %q, want %q", tc.got, tc.want)
		}
	}
}

func TestEmitCSVOmitsFooter(t *testing.T) {
	table := &Table{
		Columns: []Column{{Name: "query"}, {Name: "clicks", Right: true}},
		Rows:    [][]string{{"温泉 旅館", "100"}},
		Footer:  []string{"合計 (1行)", "100"},
		Note:    "https://example.com/",
	}
	var buf bytes.Buffer
	if err := Emit(&buf, Result{Table: table}, Options{Format: FormatCSV}); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if want := "query,clicks\n温泉 旅館,100\n"; got != want {
		t.Fatalf("got %q, want %q (no footer, no note)", got, want)
	}
}

func TestEmitCSVBOM(t *testing.T) {
	table := &Table{Columns: []Column{{Name: "a"}}, Rows: [][]string{{"1"}}}
	var buf bytes.Buffer
	if err := Emit(&buf, Result{Table: table}, Options{Format: FormatCSV, BOM: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(buf.String(), "\ufeff") {
		t.Fatal("expected a UTF-8 BOM at the start of the CSV")
	}
}

// Truncation is a display concern: CSV and JSON must keep the full value.
func TestTruncationAppliesToTableOnly(t *testing.T) {
	long := strings.Repeat("あ", 40)
	table := &Table{Columns: []Column{{Name: "query"}}, Rows: [][]string{{long}}}

	var tbl bytes.Buffer
	if err := Emit(&tbl, Result{Table: table}, Options{Format: FormatTable, MaxCellWidth: 10}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tbl.String(), "…") {
		t.Error("expected the table cell to be truncated with an ellipsis")
	}

	var csv bytes.Buffer
	if err := Emit(&csv, Result{Table: table}, Options{Format: FormatCSV, MaxCellWidth: 10}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(csv.String(), long) {
		t.Error("CSV must carry the full, untruncated value")
	}
}

// Japanese text is double-width in a terminal; columns must line up anyway.
func TestTableAlignsWideCharacters(t *testing.T) {
	table := &Table{
		Columns: []Column{{Name: "query"}, {Name: "clicks", Right: true}},
		Rows:    [][]string{{"温泉", "5"}, {"ab", "1000"}},
	}
	var buf bytes.Buffer
	if err := Emit(&buf, Result{Table: table}, Options{Format: FormatTable}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	// header, rule, two data rows
	if len(lines) < 4 {
		t.Fatalf("got %d lines, want at least 4:\n%s", len(lines), buf.String())
	}
	// "温泉" is 4 cells wide, "ab" is 2, so the "ab" row needs 2 extra spaces to
	// put both click counts in the same column.
	if !strings.HasSuffix(lines[2], "     5") || !strings.HasSuffix(lines[3], "  1000") {
		t.Errorf("columns did not line up:\n%s", buf.String())
	}
}

func TestEmitJSON(t *testing.T) {
	var buf bytes.Buffer
	payload := map[string]any{"site": "https://example.com/", "rows": []map[string]any{{"query": "温泉&旅館"}}}
	if err := Emit(&buf, Result{JSON: payload}, Options{Format: FormatJSON}); err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if !strings.Contains(buf.String(), "温泉&旅館") {
		t.Error("expected the ampersand to stay unescaped and readable")
	}
}

func TestEmitUnknownFormat(t *testing.T) {
	var buf bytes.Buffer
	if err := Emit(&buf, Result{Table: &Table{}}, Options{Format: "yaml"}); err == nil {
		t.Fatal("expected an error for an unknown format")
	}
}
