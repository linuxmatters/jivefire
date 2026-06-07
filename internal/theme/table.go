package theme

import (
	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
)

// BorderlessTable returns a lipgloss table with all borders and column dividers
// off, so it provides column alignment only (not chrome). Callers attach a
// StyleFunc to apply per-column cell styling. Shared by the TUI completion
// summary and the CLI help output to keep their column layout consistent.
func BorderlessTable() *table.Table {
	return table.New().
		Border(lipgloss.HiddenBorder()).
		BorderTop(false).
		BorderBottom(false).
		BorderLeft(false).
		BorderRight(false).
		BorderHeader(false).
		BorderColumn(false).
		BorderRow(false)
}
