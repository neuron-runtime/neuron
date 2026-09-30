package render

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

// Column defines the configuration for a table column, including its header
// title and an optional fixed width. A width of 0 lets the renderer derive the
// width from the cell contents and the available terminal space.
type Column struct {
	Title string
	Width int
}

// TableOptions defines styling and layout configurations for rendering tables.
type TableOptions struct {
	Title         string
	BorderColor   string
	HeaderBg      string
	HeaderFg      string
	Alternate     bool
	MaxTotalWidth int
}

// DefaultTableOptions returns the shared table styling. Colours are the CLI
// palette and are intentionally not configurable; the table is presentation,
// not part of any contract.
func DefaultTableOptions() TableOptions {
	return TableOptions{
		BorderColor:   "#3F3F46",
		HeaderBg:      "#27272A",
		HeaderFg:      "#F3F4F6",
		Alternate:     true,
		MaxTotalWidth: 0,
	}
}

// RenderTable writes a bordered table to out, sized to the terminal when one is
// attached and to a conservative default when output is piped or redirected.
func RenderTable(out io.Writer, columns []Column, rows [][]string, opts TableOptions) error {
	if len(columns) == 0 {
		return fmt.Errorf("table must have at least one column")
	}

	terminalWidth := 80
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 20 {
		terminalWidth = w
	}

	totalWidth := opts.MaxTotalWidth
	if totalWidth <= 0 || totalWidth > terminalWidth {
		totalWidth = terminalWidth
	}

	colWidths := make([]int, len(columns))
	unassignedCols := 0
	assignedWidthTotal := 0

	for i, col := range columns {
		if col.Width > 0 {
			colWidths[i] = col.Width
			assignedWidthTotal += col.Width
		} else {
			unassignedCols++
		}
	}

	for i, col := range columns {
		if col.Width == 0 {
			maxLen := len([]rune(col.Title))
			for _, row := range rows {
				if i < len(row) && len([]rune(row[i])) > maxLen {
					maxLen = len([]rune(row[i]))
				}
			}
			colWidths[i] = maxLen + 2
			assignedWidthTotal += colWidths[i]
		}
	}

	bordersOverhead := len(columns) + 1
	colsToStretch := unassignedCols
	if colsToStretch == 0 {
		colsToStretch = len(columns)
	}

	remainingSpace := totalWidth - (assignedWidthTotal + bordersOverhead)

	if remainingSpace > 0 {
		perColExtra := remainingSpace / colsToStretch
		remainder := remainingSpace % colsToStretch

		idx := 0
		for i, col := range columns {
			if unassignedCols == 0 || col.Width == 0 {
				colWidths[i] += perColExtra
				if idx < remainder {
					colWidths[i]++
				}
				idx++
			}
		}
	} else if assignedWidthTotal+bordersOverhead > totalWidth {
		availableSpace := max(totalWidth-bordersOverhead, len(columns)*6)
		perCol := availableSpace / len(columns)
		remainder := availableSpace % len(columns)
		for i := range colWidths {
			colWidths[i] = perCol
			if i < remainder {
				colWidths[i]++
			}
		}
	}

	borderStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(opts.BorderColor))

	baseHeaderStyle := lipgloss.NewStyle().
		Bold(true).
		Background(lipgloss.Color(opts.HeaderBg)).
		Foreground(lipgloss.Color(opts.HeaderFg)).
		Padding(0, 1).
		MaxHeight(1)

	baseCellStyle := lipgloss.NewStyle().
		Padding(0, 1).
		Foreground(lipgloss.Color("#E5E7EB")).
		MaxHeight(1)

	baseAltCellStyle := lipgloss.NewStyle().
		Padding(0, 1).
		Foreground(lipgloss.Color("#D1D5DB")).
		Background(lipgloss.Color("#18181B")).
		MaxHeight(1)

	var renderedTable strings.Builder

	if opts.Title != "" {
		titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#38BDF8")).MarginBottom(1)
		renderedTable.WriteString(titleStyle.Render(opts.Title) + "\n")
	}

	var topSep []string
	for _, w := range colWidths {
		topSep = append(topSep, strings.Repeat("─", w))
	}
	renderedTable.WriteString(borderStyle.Render("╭") + borderStyle.Render(strings.Join(topSep, "┬")) + borderStyle.Render("╮") + "\n")

	var headerCells []string
	for i, col := range columns {
		headerCells = append(headerCells, baseHeaderStyle.Width(colWidths[i]).Render(col.Title))
	}
	renderedTable.WriteString(borderStyle.Render("│") + lipgloss.JoinHorizontal(lipgloss.Top, joinRunes(headerCells, borderStyle.Render("│"))...) + borderStyle.Render("│") + "\n")

	var midSep []string
	for _, w := range colWidths {
		midSep = append(midSep, strings.Repeat("─", w))
	}
	renderedTable.WriteString(borderStyle.Render("├") + borderStyle.Render(strings.Join(midSep, "┼")) + borderStyle.Render("┤") + "\n")

	for rIdx, row := range rows {
		var rowCells []string
		isAlt := opts.Alternate && (rIdx%2 == 1)

		for i, w := range colWidths {
			val := ""
			if i < len(row) {
				val = row[i]
			}

			if isAlt {
				rowCells = append(rowCells, baseAltCellStyle.Width(w).Render(val))
			} else {
				rowCells = append(rowCells, baseCellStyle.Width(w).Render(val))
			}
		}
		renderedTable.WriteString(borderStyle.Render("│") + lipgloss.JoinHorizontal(lipgloss.Top, joinRunes(rowCells, borderStyle.Render("│"))...) + borderStyle.Render("│") + "\n")
	}

	var botSep []string
	for _, w := range colWidths {
		botSep = append(botSep, strings.Repeat("─", w))
	}
	renderedTable.WriteString(borderStyle.Render("╰") + borderStyle.Render(strings.Join(botSep, "┴")) + borderStyle.Render("╯") + "\n")

	_, err := fmt.Fprint(out, renderedTable.String())
	return err
}

// joinRunes interleaves sep between parts, producing the cell and separator
// sequence that lipgloss.JoinHorizontal expects.
func joinRunes(parts []string, sep string) []string {
	var result []string
	for i, p := range parts {
		result = append(result, p)
		if i < len(parts)-1 {
			result = append(result, sep)
		}
	}
	return result
}
