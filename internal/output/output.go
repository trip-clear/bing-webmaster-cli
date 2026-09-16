// Package output renders command results as a terminal table, CSV or JSON.
//
// Every command builds both a Table (what a human reads) and a JSON value (what
// a script or an agent parses), so the two never drift apart.
package output

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/mattn/go-runewidth"
)

// bom is the UTF-8 byte order mark, written before CSV when --bom is set.
const bom = "\ufeff"

// Formats accepted by --output.
const (
	FormatTable = "table"
	FormatJSON  = "json"
	FormatCSV   = "csv"
)

// Column is one column of a Table.
type Column struct {
	// Name is the header a human reads, in the table.
	Name string
	// Key is the header a machine reads, in CSV. It matches the JSON field of
	// the same value, so CSV and JSON exports of one query line up
	// (query,clicks,impressions,ctr,position). Falls back to Name.
	Key   string
	Right bool // right-align (numbers)
}

func (c Column) key() string {
	if c.Key != "" {
		return c.Key
	}
	return c.Name
}

// Table is the human-readable view of a result.
type Table struct {
	Columns []Column
	Rows    [][]string
	// Footer is an optional summary row (e.g. totals). It is rendered under a
	// rule in table mode and omitted from CSV, where it would corrupt the data.
	Footer []string
	// Note is printed under the table (e.g. the date range that was used).
	Note string
}

// Result is what a command hands to Emit.
type Result struct {
	Table *Table
	JSON  any
}

// Options tune rendering.
type Options struct {
	Format string
	// BOM writes a UTF-8 byte order mark before CSV, which is what Excel on
	// Japanese Windows needs to not mangle the encoding.
	BOM bool
	// MaxCellWidth truncates long cells (URLs, queries) in table mode only.
	// 0 disables truncation. CSV and JSON always carry the full value.
	MaxCellWidth int
}

// Emit renders r in the requested format.
func Emit(w io.Writer, r Result, o Options) error {
	switch o.Format {
	case FormatJSON:
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false) // URLs and queries stay readable
		return enc.Encode(r.JSON)
	case FormatCSV:
		return emitCSV(w, r.Table, o)
	case FormatTable, "":
		return emitTable(w, r.Table, o)
	default:
		return fmt.Errorf("不明な --output %q（有効: table, json, csv）", o.Format)
	}
}

func emitCSV(w io.Writer, t *Table, o Options) error {
	if t == nil {
		return nil
	}
	if o.BOM {
		if _, err := io.WriteString(w, bom); err != nil {
			return err
		}
	}
	cw := csv.NewWriter(w)
	header := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		header[i] = c.key()
	}
	if err := cw.Write(header); err != nil {
		return err
	}
	if err := cw.WriteAll(t.Rows); err != nil {
		return err
	}
	cw.Flush()
	return cw.Error()
}

func emitTable(w io.Writer, t *Table, o Options) error {
	if t == nil {
		return nil
	}
	if len(t.Rows) == 0 {
		_, err := fmt.Fprintf(w, "行が0件でした。%s\n", note(t))
		return err
	}

	rows := t.Rows
	if o.MaxCellWidth > 0 {
		rows = make([][]string, len(t.Rows))
		for i, row := range t.Rows {
			cells := make([]string, len(row))
			for j, c := range row {
				cells[j] = runewidth.Truncate(c, o.MaxCellWidth, "…")
			}
			rows[i] = cells
		}
	}

	// Widths are measured in terminal cells, not runes: Japanese queries are
	// double-width and would otherwise skew every column after them.
	widths := make([]int, len(t.Columns))
	for i, c := range t.Columns {
		widths[i] = runewidth.StringWidth(c.Name)
	}
	measure := func(row []string) {
		for i, cell := range row {
			if i < len(widths) {
				widths[i] = max(widths[i], runewidth.StringWidth(cell))
			}
		}
	}
	for _, row := range rows {
		measure(row)
	}
	if t.Footer != nil {
		measure(t.Footer)
	}

	var b strings.Builder
	writeRow := func(cells []string) {
		for i, cell := range cells {
			if i >= len(t.Columns) {
				break
			}
			if i > 0 {
				b.WriteString("  ")
			}
			b.WriteString(pad(cell, widths[i], t.Columns[i].Right))
		}
		b.WriteByte('\n')
	}

	header := make([]string, len(t.Columns))
	rule := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		header[i] = c.Name
		rule[i] = strings.Repeat("-", widths[i])
	}
	writeRow(header)
	writeRow(rule)
	for _, row := range rows {
		writeRow(row)
	}
	if t.Footer != nil {
		writeRow(rule)
		writeRow(t.Footer)
	}
	if n := note(t); n != "" {
		b.WriteString("\n" + n + "\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func note(t *Table) string {
	if t == nil {
		return ""
	}
	return t.Note
}

// pad aligns a cell to width, measuring in terminal cells.
func pad(s string, width int, right bool) string {
	gap := width - runewidth.StringWidth(s)
	if gap < 0 {
		gap = 0
	}
	if right {
		return strings.Repeat(" ", gap) + s
	}
	return s + strings.Repeat(" ", gap)
}

// Int formats a metric that is conceptually a count (the API returns float64).
func Int(v float64) string { return fmt.Sprintf("%.0f", v) }

// Pct formats a ratio (0.0731) as a percentage ("7.31%").
func Pct(v float64) string { return fmt.Sprintf("%.2f%%", v*100) }

// Pos formats an average position.
func Pos(v float64) string { return fmt.Sprintf("%.1f", v) }

// DeltaInt formats an absolute change with an explicit sign.
func DeltaInt(cur, prev float64) string { return withSign(cur-prev, 0) }

// DeltaPct formats a change in a ratio, in percentage points.
func DeltaPct(cur, prev float64) string { return withSign((cur-prev)*100, 2) + "pt" }

// DeltaPos formats a change in average position. Lower is better, so the sign
// is inverted: an improvement from 12.0 to 8.0 shows as "+4.0".
func DeltaPos(cur, prev float64) string { return withSign(prev-cur, 1) }

// Growth formats the relative change ("+38.2%"), or "new" when there is no
// baseline to divide by.
func Growth(cur, prev float64) string {
	if prev == 0 {
		if cur == 0 {
			return "-"
		}
		return "new"
	}
	return withSign((cur-prev)/prev*100, 1) + "%"
}

func withSign(v float64, decimals int) string {
	if math.Abs(v) < math.Pow10(-decimals)/2 {
		v = 0 // avoid rendering "-0.0"
	}
	s := fmt.Sprintf("%+.*f", decimals, v)
	if v == 0 {
		return fmt.Sprintf("%.*f", decimals, 0.0)
	}
	return s
}
