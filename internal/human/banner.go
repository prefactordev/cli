package human

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// glyphs are the letters of PREFACTOR in the ANSI Shadow figlet font.
var glyphs = map[rune][6]string{
	'P': {"██████╗ ", "██╔══██╗", "██████╔╝", "██╔═══╝ ", "██║     ", "╚═╝     "},
	'R': {"██████╗ ", "██╔══██╗", "██████╔╝", "██╔══██╗", "██║  ██║", "╚═╝  ╚═╝"},
	'E': {"███████╗", "██╔════╝", "█████╗  ", "██╔══╝  ", "███████╗", "╚══════╝"},
	'F': {"███████╗", "██╔════╝", "█████╗  ", "██╔══╝  ", "██║     ", "╚═╝     "},
	'A': {" █████╗ ", "██╔══██╗", "███████║", "██╔══██║", "██║  ██║", "╚═╝  ╚═╝"},
	'C': {" ██████╗", "██╔════╝", "██║     ", "██║     ", "╚██████╗", " ╚═════╝"},
	'T': {"████████╗", "╚══██╔══╝", "   ██║   ", "   ██║   ", "   ██║   ", "   ╚═╝   "},
	'O': {" ██████╗ ", "██╔═══██╗", "██║   ██║", "██║   ██║", "╚██████╔╝", " ╚═════╝ "},
}

const bannerWord = "PREFACTOR"

// BannerWidth is the banner's width in columns.
func BannerWidth() int {
	width := 0
	for _, r := range bannerWord {
		width += len([]rune(glyphs[r][0]))
	}
	return width
}

// Banner draws PREFACTOR in block letters shaded from violet to sky blue,
// one colour per column.
func Banner() string {
	rows := make([][]rune, 6)
	for _, r := range bannerWord {
		for i, line := range glyphs[r] {
			rows[i] = append(rows[i], []rune(line)...)
		}
	}
	colours := lipgloss.Blend1D(len(rows[0]), violet, indigo, sky)

	var b strings.Builder
	for _, row := range rows {
		for col, r := range row {
			if r == ' ' {
				b.WriteRune(r)
				continue
			}
			b.WriteString(lipgloss.NewStyle().Foreground(colours[col]).Render(string(r)))
		}
		b.WriteByte('\n')
	}
	return b.String()
}
