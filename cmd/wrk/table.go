package main

import (
	"io"
	"strings"
	"unicode/utf8"
)

// alignedCell is one cell of a table row. Content is the exact string
// emitted (may contain ANSI escape sequences); width is the number of
// *visible* runes — i.e. the plain-text form's rune count, used for
// column-width computation and padding.
//
// Splitting the two apart is the reason we cannot use `text/tabwriter`
// for colored tables: tabwriter counts every byte, including ANSI
// escape sequences, toward column width. That padding is invisible on
// the terminal but shifts the header row 9 columns to the right of the
// data rows.
type alignedCell struct {
	content string
	width   int
}

// alignedRow is a table row consisting of cells emitted left to right.
type alignedRow []alignedCell

// plainCell is a cell whose content has no ANSI escapes — the visible
// width equals the rune count of the content.
func plainCell(s string) alignedCell {
	return alignedCell{content: s, width: utf8.RuneCountInString(s)}
}

// plainRow builds an alignedRow from a list of plain-text cells.
func plainRow(cells []string) alignedRow {
	row := make(alignedRow, len(cells))
	for i, c := range cells {
		row[i] = plainCell(c)
	}
	return row
}

// coloredCell pairs a rendered string (may contain ANSI escapes) with
// the visible-rune width derived from its plain-text counterpart.
func coloredCell(rendered, plain string) alignedCell {
	return alignedCell{content: rendered, width: utf8.RuneCountInString(plain)}
}

// writeAligned emits rows to w with each column padded to the widest
// visible cell in that column. Two spaces separate columns, matching
// the tabwriter defaults previously used elsewhere in this package.
//
// The last column of every row is written without trailing padding so
// stray whitespace does not accumulate at end of line.
func writeAligned(w io.Writer, rows []alignedRow) error {
	if len(rows) == 0 {
		return nil
	}

	ncols := 0
	for _, row := range rows {
		if len(row) > ncols {
			ncols = len(row)
		}
	}
	widths := make([]int, ncols)
	for _, row := range rows {
		for i, cell := range row {
			if cell.width > widths[i] {
				widths[i] = cell.width
			}
		}
	}

	var buf strings.Builder
	for _, row := range rows {
		for i, cell := range row {
			buf.WriteString(cell.content)
			if i < len(row)-1 {
				pad := widths[i] - cell.width + 2
				if pad < 0 {
					pad = 0
				}
				buf.WriteString(strings.Repeat(" ", pad))
			}
		}
		buf.WriteByte('\n')
	}
	_, err := io.WriteString(w, buf.String())
	return err
}
