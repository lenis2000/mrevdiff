package ui

import "github.com/charmbracelet/lipgloss"

// Styles bundles the lipgloss styles used across panes. The palette mirrors
// vim's default colorscheme: Normal on black, Comment cyan italic,
// Statement yellow (→ \commands), PreProc magenta bold (→ structural
// keywords), Type green (→ env names), Constant/Number light red, Special
// red for math delimiters. StatusLine uses a reverse-ish treatment (white
// on dark blue) to match vim's default StatusLine group.
type Styles struct {
	Pane         lipgloss.Style
	PaneFocused  lipgloss.Style
	PaneTitle    lipgloss.Style
	StatusBar    lipgloss.Style
	StatusKey    lipgloss.Style
	StatusFilter lipgloss.Style

	// Outline pane.
	OutlineIcon   lipgloss.Style
	OutlineMarker lipgloss.Style
	OutlineCursor lipgloss.Style
	OutlineActive lipgloss.Style
	OutlineMuted  lipgloss.Style

	// Source pane — vim-like LaTeX highlighting. Command / keyword /
	// math / env-name / brace / number / math-content get distinct
	// colours; comments stay muted italic. SourceAnnotation is the
	// inline (line-pinned) note style; SourceAnnotationBlock paints
	// paragraph-level (block-scope) notes in a distinct hue so the two
	// kinds are tellable apart at a glance.
	SourceGutter          lipgloss.Style
	SourceComment         lipgloss.Style
	SourceCommand         lipgloss.Style
	SourceKeyword         lipgloss.Style
	SourceMath            lipgloss.Style
	SourceMathText        lipgloss.Style
	SourceEnvName         lipgloss.Style
	SourceBrace           lipgloss.Style
	SourceNumber          lipgloss.Style
	SourceAnnotation      lipgloss.Style
	SourceAnnotationBlock lipgloss.Style
}

// vim-default 256-colour approximations. Names match the Vim highlight
// group they're conceptually linked to so tweaking is intuitive later.
const (
	vimBg        = "16"  // Normal bg — true black
	vimFg        = "231" // Normal fg — pure white
	vimLineNr    = "240" // LineNr — mid grey
	vimVertSplit = "238" // VertSplit — dim border
	vimCursor    = "238" // CursorLine bg — subtle highlight
	vimStatusBg  = "17"  // StatusLine bg — dark blue (vim default is "User1")
	vimStatusFg  = "231" // StatusLine fg — white
	vimComment   = "51"  // Comment — light cyan (vim: Cyan italic)
	vimStmt      = "228" // Statement — yellow (soft, so math bold can pop harder)
	vimPreProc   = "213" // PreProc — magenta/pink
	vimType      = "120" // Type — light green
	vimConstant  = "210" // Constant / Number — light red-orange
	vimSpecial   = "209" // Special — orange (used for math delims)
	vimString    = "216" // String — peach (used for env names / math content)
	vimSearch    = "226" // Search / hit highlight — bright yellow
	vimAnnBlock  = "150" // Paragraph-annotation bg — soft yellow-green
)

// DefaultStyles returns the vim-default-dark palette. Colors are 256-index
// so the look is consistent across terminals without truecolor.
func DefaultStyles() Styles {
	pane := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color(vimVertSplit)).
		Background(lipgloss.Color(vimBg)).
		Foreground(lipgloss.Color(vimFg))
	focus := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("39")). // brighter blue for focus
		Background(lipgloss.Color(vimBg)).
		Foreground(lipgloss.Color(vimFg))
	return Styles{
		Pane:         pane,
		PaneFocused:  focus,
		PaneTitle:    lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(vimStmt)).Background(lipgloss.Color(vimBg)),
		StatusBar:    lipgloss.NewStyle().Foreground(lipgloss.Color(vimStatusFg)).Background(lipgloss.Color(vimStatusBg)).Bold(true),
		StatusKey:    lipgloss.NewStyle().Foreground(lipgloss.Color(vimSpecial)).Bold(true).Background(lipgloss.Color(vimStatusBg)),
		StatusFilter: lipgloss.NewStyle().Foreground(lipgloss.Color(vimType)).Background(lipgloss.Color(vimStatusBg)),

		OutlineIcon:   lipgloss.NewStyle().Foreground(lipgloss.Color(vimPreProc)).Background(lipgloss.Color(vimBg)),
		OutlineMarker: lipgloss.NewStyle().Foreground(lipgloss.Color(vimStmt)).Background(lipgloss.Color(vimBg)),
		OutlineCursor: lipgloss.NewStyle().Foreground(lipgloss.Color(vimFg)).Background(lipgloss.Color(vimCursor)).Bold(true),
		OutlineActive: lipgloss.NewStyle().Foreground(lipgloss.Color(vimFg)).Background(lipgloss.Color("236")),
		OutlineMuted:  lipgloss.NewStyle().Foreground(lipgloss.Color(vimLineNr)).Background(lipgloss.Color(vimBg)),

		SourceGutter:          lipgloss.NewStyle().Foreground(lipgloss.Color(vimLineNr)).Background(lipgloss.Color(vimBg)),
		SourceComment:         lipgloss.NewStyle().Foreground(lipgloss.Color(vimComment)).Italic(true).Background(lipgloss.Color(vimBg)),
		SourceCommand:         lipgloss.NewStyle().Foreground(lipgloss.Color(vimStmt)).Background(lipgloss.Color(vimBg)),
		SourceKeyword:         lipgloss.NewStyle().Foreground(lipgloss.Color(vimPreProc)).Bold(true).Background(lipgloss.Color(vimBg)),
		SourceMath:            lipgloss.NewStyle().Foreground(lipgloss.Color(vimSpecial)).Bold(true).Background(lipgloss.Color(vimBg)),
		SourceMathText:        lipgloss.NewStyle().Foreground(lipgloss.Color(vimString)).Background(lipgloss.Color(vimBg)),
		SourceEnvName:         lipgloss.NewStyle().Foreground(lipgloss.Color(vimType)).Background(lipgloss.Color(vimBg)),
		SourceBrace:           lipgloss.NewStyle().Foreground(lipgloss.Color(vimLineNr)).Background(lipgloss.Color(vimBg)),
		SourceNumber:          lipgloss.NewStyle().Foreground(lipgloss.Color(vimConstant)).Background(lipgloss.Color(vimBg)),
		SourceAnnotation:      lipgloss.NewStyle().Foreground(lipgloss.Color(vimBg)).Background(lipgloss.Color(vimSearch)).Italic(true),
		SourceAnnotationBlock: lipgloss.NewStyle().Foreground(lipgloss.Color(vimBg)).Background(lipgloss.Color(vimAnnBlock)).Italic(true),
	}
}

// vim-default light-background approximations.
const (
	vimBgLight   = "231" // Normal bg — white
	vimFgLight   = "16"  // Normal fg — black
	vimLineNrL   = "250"
	vimBorderL   = "245"
	vimCursorL   = "254" // very light grey
	vimStatusBgL = "25"  // dark blue
	vimStatusFgL = "231" // white
	vimCommentL  = "24"  // dark cyan
	vimStmtL     = "130" // dark orange/brown
	vimPreProcL  = "90"  // dark magenta
	vimTypeL     = "22"  // dark green
	vimConstantL = "124" // dark red
	vimSpecialL  = "160" // brighter red
	vimStringL   = "94"  // brown/olive
	vimSearchBgL = "226"
	vimAnnBlockL = "150" // Paragraph-annotation bg — soft yellow-green
)

// lightStyles returns a palette tuned for light-background terminals. The
// structural roles (pane border, status bar, cursor row) are unchanged —
// only the colour indices shift so foregrounds remain legible on a pale
// backdrop. Mirrors vim's default colorscheme for light bg.
func lightStyles() Styles {
	pane := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color(vimBorderL)).
		Background(lipgloss.Color(vimBgLight)).
		Foreground(lipgloss.Color(vimFgLight))
	focus := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("26")).
		Background(lipgloss.Color(vimBgLight)).
		Foreground(lipgloss.Color(vimFgLight))
	return Styles{
		Pane:         pane,
		PaneFocused:  focus,
		PaneTitle:    lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(vimStmtL)).Background(lipgloss.Color(vimBgLight)),
		StatusBar:    lipgloss.NewStyle().Foreground(lipgloss.Color(vimStatusFgL)).Background(lipgloss.Color(vimStatusBgL)).Bold(true),
		StatusKey:    lipgloss.NewStyle().Foreground(lipgloss.Color("208")).Bold(true).Background(lipgloss.Color(vimStatusBgL)),
		StatusFilter: lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Background(lipgloss.Color(vimStatusBgL)),

		OutlineIcon:   lipgloss.NewStyle().Foreground(lipgloss.Color(vimPreProcL)).Background(lipgloss.Color(vimBgLight)),
		OutlineMarker: lipgloss.NewStyle().Foreground(lipgloss.Color(vimStmtL)).Background(lipgloss.Color(vimBgLight)),
		OutlineCursor: lipgloss.NewStyle().Foreground(lipgloss.Color(vimFgLight)).Background(lipgloss.Color(vimCursorL)).Bold(true),
		OutlineActive: lipgloss.NewStyle().Foreground(lipgloss.Color(vimFgLight)).Background(lipgloss.Color("253")),
		OutlineMuted:  lipgloss.NewStyle().Foreground(lipgloss.Color(vimLineNrL)).Background(lipgloss.Color(vimBgLight)),

		SourceGutter:          lipgloss.NewStyle().Foreground(lipgloss.Color(vimLineNrL)).Background(lipgloss.Color(vimBgLight)),
		SourceComment:         lipgloss.NewStyle().Foreground(lipgloss.Color(vimCommentL)).Italic(true).Background(lipgloss.Color(vimBgLight)),
		SourceCommand:         lipgloss.NewStyle().Foreground(lipgloss.Color(vimStmtL)).Background(lipgloss.Color(vimBgLight)),
		SourceKeyword:         lipgloss.NewStyle().Foreground(lipgloss.Color(vimPreProcL)).Bold(true).Background(lipgloss.Color(vimBgLight)),
		SourceMath:            lipgloss.NewStyle().Foreground(lipgloss.Color(vimSpecialL)).Bold(true).Background(lipgloss.Color(vimBgLight)),
		SourceMathText:        lipgloss.NewStyle().Foreground(lipgloss.Color(vimStringL)).Background(lipgloss.Color(vimBgLight)),
		SourceEnvName:         lipgloss.NewStyle().Foreground(lipgloss.Color(vimTypeL)).Background(lipgloss.Color(vimBgLight)),
		SourceBrace:           lipgloss.NewStyle().Foreground(lipgloss.Color(vimLineNrL)).Background(lipgloss.Color(vimBgLight)),
		SourceNumber:          lipgloss.NewStyle().Foreground(lipgloss.Color(vimConstantL)).Background(lipgloss.Color(vimBgLight)),
		SourceAnnotation:      lipgloss.NewStyle().Foreground(lipgloss.Color(vimFgLight)).Background(lipgloss.Color(vimSearchBgL)).Italic(true),
		SourceAnnotationBlock: lipgloss.NewStyle().Foreground(lipgloss.Color(vimFgLight)).Background(lipgloss.Color(vimAnnBlockL)).Italic(true),
	}
}
