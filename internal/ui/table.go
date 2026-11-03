package ui

import "strings"

// Cell is one table cell: its text, and how it is painted (nil = plain). The colour goes on AFTER the padding is counted,
// so an escape code never moves a column.
type Cell struct {
	Text  string
	Paint func(string) string
}

// Table is rows under a dim header, each line starting with indent, the columns two spaces apart and the last one not
// padded. A table is never folded: a row that does not fit runs on, which keeps its columns readable as columns.
func (m Mode) Table(indent string, header []string, rows [][]Cell) []string {
	widths := make([]int, len(header))
	for i, h := range header {
		widths[i] = Columns(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) {
				widths[i] = max(widths[i], Columns(cell.Text))
			}
		}
	}
	line := func(cells []Cell) string {
		var b strings.Builder
		b.WriteString(indent)
		for i, cell := range cells {
			text := cell.Text
			if i < len(cells)-1 {
				text += strings.Repeat(" ", widths[i]-Columns(cell.Text)) + "  "
				b.WriteString(paintBody(cell.Text, cell.Paint) + text[len(cell.Text):])
				continue
			}
			b.WriteString(paintBody(text, cell.Paint))
		}
		return strings.TrimRight(b.String(), " ")
	}
	heads := make([]Cell, len(header))
	for i, h := range header {
		heads[i] = Cell{Text: h, Paint: m.Dim}
	}
	out := []string{line(heads)}
	for _, row := range rows {
		out = append(out, line(row))
	}
	return out
}
