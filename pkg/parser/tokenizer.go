// Package parser tokenizes and parses LaTeX sources into the block model used by mrevdiff.
package parser

import (
	"bytes"
	"strings"
)

// TokenKind is the category of a lexical token produced by Tokenize.
type TokenKind int

const (
	TokBeginEnv TokenKind = iota
	TokEndEnv
	TokSection
	TokLabel
	TokRef
	TokDisplayOpen
	TokDisplayClose
	TokNewTheorem
	TokTheoremStyle
	TokBlankLine
	TokCommentLine
	TokItem
)

// String returns a short human-readable name for a TokenKind.
func (k TokenKind) String() string {
	switch k {
	case TokBeginEnv:
		return "BeginEnv"
	case TokEndEnv:
		return "EndEnv"
	case TokSection:
		return "Section"
	case TokLabel:
		return "Label"
	case TokRef:
		return "Ref"
	case TokDisplayOpen:
		return "DisplayOpen"
	case TokDisplayClose:
		return "DisplayClose"
	case TokNewTheorem:
		return "NewTheorem"
	case TokTheoremStyle:
		return "TheoremStyle"
	case TokBlankLine:
		return "BlankLine"
	case TokCommentLine:
		return "CommentLine"
	case TokItem:
		return "Item"
	}
	return "Unknown"
}

// Token is a single lexical unit emitted by Tokenize. Unused fields are
// zero-valued; the interpretation of Title/Target/EnvName/etc. depends on Kind.
type Token struct {
	Kind    TokenKind
	Line    int // 1-based line number
	Col     int // 1-based column of the token start
	EnvName string
	Title   string
	Target  string
	RefKind string
	Level   int
	Starred bool
	Chain   string
}

// sectionLevels maps section-like commands to a depth (1=section, 2=subsection, ...).
var sectionLevels = map[string]int{
	"part":          0,
	"chapter":       0,
	"section":       1,
	"subsection":    2,
	"subsubsection": 3,
	"paragraph":     4,
	"subparagraph":  5,
}

// skipEnvs are environments whose body is not tokenized (verbatim-like).
var skipEnvs = map[string]bool{
	"verbatim":   true,
	"verbatim*":  true,
	"Verbatim":   true, // fancyvrb / fvextra
	"Verbatim*":  true,
	"lstlisting": true,
	"minted":     true, // Pygments-backed listings
	"comment":    true,
}

// refCmds maps ref-producing commands to the RefKind recorded on the token.
var refCmds = map[string]string{
	"ref":   "ref",
	"cref":  "cref",
	"Cref":  "Cref",
	"eqref": "eqref",
	"cite":  "cite",
}

// Tokenize scans the LaTeX source and returns a flat stream of tokens.
// Line/column positions are 1-based. Content inside verbatim-like environments
// is skipped; % comments are stripped but do not shift positions of other tokens.
func Tokenize(src []byte) []Token {
	s := &scanner{src: src, line: 1}
	s.run()
	return s.tokens
}

type scanner struct {
	src          []byte
	pos          int
	line         int
	tokens       []Token
	skipEnv      string
	inDollarMath bool // currently inside $$...$$ (may span lines)
}

func (s *scanner) run() {
	for s.pos < len(s.src) {
		lineStart := s.pos
		lineEnd := s.findLineEnd()
		line := s.src[lineStart:lineEnd]

		if s.skipEnv != "" {
			// Inside verbatim-like env: look only for matching \end{...} on this line.
			marker := []byte(`\end{` + s.skipEnv + `}`)
			if idx := bytes.Index(line, marker); idx >= 0 {
				s.tokens = append(s.tokens, Token{
					Kind: TokEndEnv, EnvName: s.skipEnv,
					Line: s.line, Col: idx + 1,
				})
				s.skipEnv = ""
			}
		} else {
			trimmed := bytes.TrimLeft(line, " \t")
			switch {
			case len(trimmed) == 0:
				s.tokens = append(s.tokens, Token{Kind: TokBlankLine, Line: s.line, Col: 1})
			case trimmed[0] == '%':
				s.tokens = append(s.tokens, Token{Kind: TokCommentLine, Line: s.line, Col: 1})
			default:
				s.scanLine(line)
			}
		}

		if lineEnd < len(s.src) {
			s.pos = lineEnd + 1
		} else {
			s.pos = lineEnd
		}
		s.line++
	}
}

// findLineEnd returns the index of the next '\n' at or after s.pos,
// or len(s.src) if none.
func (s *scanner) findLineEnd() int {
	i := s.pos
	for i < len(s.src) && s.src[i] != '\n' {
		i++
	}
	return i
}

// scanLine walks a single line looking for inline tokens.
func (s *scanner) scanLine(line []byte) {
	i := 0
	for i < len(line) {
		if s.skipEnv != "" {
			// A BeginEnv on this line switched us into skip mode; abandon the rest.
			return
		}
		switch c := line[i]; c {
		case '%':
			return
		case '\\':
			i = s.scanBackslash(line, i)
		case '$':
			i = s.scanDollar(line, i)
		default:
			i++
		}
	}
}

// scanBackslash handles a '\' at line[i] and returns the next index.
func (s *scanner) scanBackslash(line []byte, i int) int {
	cmdStart := i
	if i+1 >= len(line) {
		return i + 1
	}
	next := line[i+1]
	switch next {
	case '[':
		s.tokens = append(s.tokens, Token{Kind: TokDisplayOpen, Line: s.line, Col: cmdStart + 1})
		return i + 2
	case ']':
		s.tokens = append(s.tokens, Token{Kind: TokDisplayClose, Line: s.line, Col: cmdStart + 1})
		return i + 2
	}
	if !isLetter(next) {
		// Escaped single char: \%, \$, \&, \#, \_, \\, \(, \), \,, etc.
		return i + 2
	}
	// Command name follows: one or more letters.
	j := i + 1
	for j < len(line) && isLetter(line[j]) {
		j++
	}
	name := string(line[i+1 : j])
	k := j
	starred := false
	if k < len(line) && line[k] == '*' {
		starred = true
		k++
	}
	return s.handleCommand(line, k, cmdStart, name, starred)
}

// scanDollar handles '$' and returns the next index. Tracks $$...$$ state
// across lines via s.inDollarMath. Single '$' denotes inline math; its body
// is skipped on the current line.
func (s *scanner) scanDollar(line []byte, i int) int {
	if i+1 < len(line) && line[i+1] == '$' {
		kind := TokDisplayOpen
		if s.inDollarMath {
			kind = TokDisplayClose
		}
		s.tokens = append(s.tokens, Token{Kind: kind, Line: s.line, Col: i + 1})
		s.inDollarMath = !s.inDollarMath
		return i + 2
	}
	// Inline math: skip to closing '$' on the same line.
	j := i + 1
	for j < len(line) && line[j] != '$' {
		if line[j] == '\\' && j+1 < len(line) {
			j += 2
			continue
		}
		j++
	}
	if j < len(line) {
		j++
	}
	return j
}

// handleCommand dispatches on the command name and consumes its arguments.
// i points just after the (optional) trailing '*'.
func (s *scanner) handleCommand(line []byte, i, cmdStart int, name string, starred bool) int {
	col := cmdStart + 1
	switch name {
	case "begin":
		env, next, ok := readBracedArg(line, i)
		if !ok {
			return i
		}
		s.tokens = append(s.tokens, Token{Kind: TokBeginEnv, EnvName: env, Line: s.line, Col: col})
		if skipEnvs[env] {
			s.skipEnv = env
		}
		return next
	case "end":
		env, next, ok := readBracedArg(line, i)
		if !ok {
			return i
		}
		s.tokens = append(s.tokens, Token{Kind: TokEndEnv, EnvName: env, Line: s.line, Col: col})
		return next
	case "label":
		lbl, next, ok := readBracedArg(line, i)
		if !ok {
			return i
		}
		s.tokens = append(s.tokens, Token{Kind: TokLabel, Target: lbl, Line: s.line, Col: col})
		return next
	case "newtheorem":
		env, next, ok := readBracedArg(line, i)
		if !ok {
			return i
		}
		chain := ""
		if next < len(line) && line[next] == '[' {
			if ch, afterCh, okCh := readBracketedArg(line, next); okCh {
				chain = ch
				next = afterCh
			}
		}
		title := ""
		if t, afterT, okT := readBracedArg(line, next); okT {
			title = t
			next = afterT
		}
		s.tokens = append(s.tokens, Token{
			Kind: TokNewTheorem, EnvName: env, Chain: chain, Title: title, Starred: starred,
			Line: s.line, Col: col,
		})
		return next
	case "theoremstyle":
		style, next, ok := readBracedArg(line, i)
		if !ok {
			return i
		}
		s.tokens = append(s.tokens, Token{Kind: TokTheoremStyle, EnvName: style, Line: s.line, Col: col})
		return next
	}
	if kind, ok := refCmds[name]; ok {
		j := i
		if name == "cite" {
			j = skipOptionalArg(line, j)
		}
		arg, next, ok := readBracedArg(line, j)
		if !ok {
			return i
		}
		for _, key := range strings.Split(arg, ",") {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			s.tokens = append(s.tokens, Token{
				Kind: TokRef, RefKind: kind, Target: key,
				Line: s.line, Col: col,
			})
		}
		return next
	}
	if lvl, ok := sectionLevels[name]; ok {
		j := skipOptionalArg(line, i)
		title, next, okT := readBracedArg(line, j)
		if !okT {
			return i
		}
		s.tokens = append(s.tokens, Token{
			Kind: TokSection, Level: lvl, Title: title, Starred: starred,
			Line: s.line, Col: col,
		})
		return next
	}
	if name == "item" {
		// Optional [label] is consumed only if it closes on the same line —
		// math papers don't break \item arguments across lines, so a stray
		// '[' is more likely raw content (e.g. \item [a,b] meaning "the pair").
		next := i
		j := i
		for j < len(line) && (line[j] == ' ' || line[j] == '\t') {
			j++
		}
		title := ""
		if j < len(line) && line[j] == '[' {
			if t, after, ok := readBracketedArg(line, j); ok {
				title = t
				next = after
			}
		}
		s.tokens = append(s.tokens, Token{
			Kind: TokItem, Title: title, Line: s.line, Col: col,
		})
		return next
	}
	return i
}

// readBracedArg skips leading whitespace and, if line[i] == '{', reads a
// balanced '{...}' block. Returns (content, index after '}', ok).
// Escaped '\{' and '\}' inside the body do not affect depth.
func readBracedArg(line []byte, i int) (string, int, bool) {
	j := i
	for j < len(line) && (line[j] == ' ' || line[j] == '\t') {
		j++
	}
	if j >= len(line) || line[j] != '{' {
		return "", i, false
	}
	depth := 1
	k := j + 1
	for k < len(line) {
		c := line[k]
		if c == '\\' && k+1 < len(line) {
			k += 2
			continue
		}
		switch c {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return string(line[j+1 : k]), k + 1, true
			}
		}
		k++
	}
	return "", i, false
}

// readBracketedArg reads a '[...]' block. Assumes line[i] == '['.
func readBracketedArg(line []byte, i int) (string, int, bool) {
	if i >= len(line) || line[i] != '[' {
		return "", i, false
	}
	depth := 1
	j := i + 1
	for j < len(line) {
		c := line[j]
		if c == '\\' && j+1 < len(line) {
			j += 2
			continue
		}
		switch c {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return string(line[i+1 : j]), j + 1, true
			}
		}
		j++
	}
	return "", i, false
}

// skipOptionalArg skips whitespace + a single optional [...] argument if
// present; otherwise returns i unchanged.
func skipOptionalArg(line []byte, i int) int {
	j := i
	for j < len(line) && (line[j] == ' ' || line[j] == '\t') {
		j++
	}
	if j < len(line) && line[j] == '[' {
		if _, after, ok := readBracketedArg(line, j); ok {
			return after
		}
	}
	return i
}

func isLetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
