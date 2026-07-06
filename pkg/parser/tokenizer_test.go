package parser

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenize_SampleFixture(t *testing.T) {
	src := readFixture(t, "sample.tex")
	tokens := Tokenize(src)

	counts := map[TokenKind]int{}
	for _, tk := range tokens {
		counts[tk.Kind]++
	}

	assert.Equal(t, 1, counts[TokSection], "expected one \\section")
	assert.Equal(t, 3, counts[TokNewTheorem], "expected three \\newtheorem declarations")
	assert.Equal(t, 1, counts[TokTheoremStyle], "expected one \\theoremstyle")
	// \label{sec:main}, \label{thm:main}, \label{fig:diagram}
	assert.Equal(t, 3, counts[TokLabel], "expected three \\label commands")
	// \[ … \] pair for summation formula
	assert.GreaterOrEqual(t, counts[TokDisplayOpen], 2)
	assert.GreaterOrEqual(t, counts[TokDisplayClose], 2)

	// BeginEnv/EndEnv must be balanced per environment name.
	envDelta := map[string]int{}
	for _, tk := range tokens {
		switch tk.Kind {
		case TokBeginEnv:
			envDelta[tk.EnvName]++
		case TokEndEnv:
			envDelta[tk.EnvName]--
		}
	}
	for env, d := range envDelta {
		assert.Equal(t, 0, d, "unbalanced env: %s", env)
	}
}

func TestTokenize_SectionFields(t *testing.T) {
	src := readFixture(t, "sample.tex")
	tokens := Tokenize(src)

	var sec *Token
	for i := range tokens {
		if tokens[i].Kind == TokSection {
			sec = &tokens[i]
			break
		}
	}
	require.NotNil(t, sec, "expected a TokSection")
	assert.Equal(t, 1, sec.Level)
	assert.Equal(t, "Main results", sec.Title)
	assert.False(t, sec.Starred)
}

func TestTokenize_NewTheoremFields(t *testing.T) {
	src := readFixture(t, "sample.tex")
	tokens := Tokenize(src)

	var got []Token
	for _, tk := range tokens {
		if tk.Kind == TokNewTheorem {
			got = append(got, tk)
		}
	}
	require.Len(t, got, 3)

	// theorem (no chain, not starred)
	assert.Equal(t, "theorem", got[0].EnvName)
	assert.Equal(t, "Theorem", got[0].Title)
	assert.Equal(t, "", got[0].Chain)
	assert.False(t, got[0].Starred)

	// lemma chained on theorem
	assert.Equal(t, "lemma", got[1].EnvName)
	assert.Equal(t, "Lemma", got[1].Title)
	assert.Equal(t, "theorem", got[1].Chain)
	assert.False(t, got[1].Starred)

	// remark (starred, no chain)
	assert.Equal(t, "remark", got[2].EnvName)
	assert.Equal(t, "Remark", got[2].Title)
	assert.True(t, got[2].Starred)
}

func TestTokenize_RefKinds(t *testing.T) {
	src := readFixture(t, "sample.tex")
	tokens := Tokenize(src)

	kinds := map[string]int{}
	targets := map[string]int{}
	for _, tk := range tokens {
		if tk.Kind != TokRef {
			continue
		}
		kinds[tk.RefKind]++
		targets[tk.Target]++
	}

	assert.GreaterOrEqual(t, kinds["ref"], 2, "expected multiple \\ref tokens")
	assert.GreaterOrEqual(t, kinds["cref"], 1, "expected a \\cref token")
	assert.Equal(t, 2, kinds["cite"], "multi-key cite should split into separate tokens")
	assert.Equal(t, 1, targets["GKP1994"])
	assert.Equal(t, 1, targets["Stanley2011"])
	assert.Equal(t, 1, targets["sec:main"])
	assert.Equal(t, 1, targets["thm:missing"])
}

func TestTokenize_LabelPositions(t *testing.T) {
	src := readFixture(t, "sample.tex")
	tokens := Tokenize(src)

	// All label targets we expect to find.
	want := map[string]bool{"sec:main": false, "thm:main": false, "fig:diagram": false}
	for _, tk := range tokens {
		if tk.Kind == TokLabel {
			if _, ok := want[tk.Target]; ok {
				want[tk.Target] = true
				assert.Greater(t, tk.Line, 0, "label token must have a positive line number")
			}
		}
	}
	for k, seen := range want {
		assert.True(t, seen, "missing label: %s", k)
	}
}

func TestTokenize_Cases(t *testing.T) {
	cases := []struct {
		name  string
		src   string
		check func(t *testing.T, tokens []Token)
	}{
		{
			name: "starred section",
			src:  `\section*{Intro}`,
			check: func(t *testing.T, tokens []Token) {
				var sec *Token
				for i := range tokens {
					if tokens[i].Kind == TokSection {
						sec = &tokens[i]
					}
				}
				require.NotNil(t, sec)
				assert.True(t, sec.Starred)
				assert.Equal(t, "Intro", sec.Title)
				assert.Equal(t, 1, sec.Level)
			},
		},
		{
			name: "section with optional short title",
			src:  `\section[short]{Long Title}`,
			check: func(t *testing.T, tokens []Token) {
				var sec *Token
				for i := range tokens {
					if tokens[i].Kind == TokSection {
						sec = &tokens[i]
					}
				}
				require.NotNil(t, sec)
				assert.Equal(t, "Long Title", sec.Title)
			},
		},
		{
			name: "multi-key cite with spaces",
			src:  `\cite{a, b , c}`,
			check: func(t *testing.T, tokens []Token) {
				var targets []string
				for _, tk := range tokens {
					if tk.Kind == TokRef {
						targets = append(targets, tk.Target)
					}
				}
				assert.Equal(t, []string{"a", "b", "c"}, targets)
			},
		},
		{
			name: "cite with optional note",
			src:  `\cite[p.~5]{smith}`,
			check: func(t *testing.T, tokens []Token) {
				var targets []string
				for _, tk := range tokens {
					if tk.Kind == TokRef {
						targets = append(targets, tk.Target)
					}
				}
				assert.Equal(t, []string{"smith"}, targets)
			},
		},
		{
			name: "double dollar display toggles",
			src:  `alpha $$x=y$$ beta $$ab$$`,
			check: func(t *testing.T, tokens []Token) {
				var kinds []TokenKind
				for _, tk := range tokens {
					if tk.Kind == TokDisplayOpen || tk.Kind == TokDisplayClose {
						kinds = append(kinds, tk.Kind)
					}
				}
				assert.Equal(t,
					[]TokenKind{TokDisplayOpen, TokDisplayClose, TokDisplayOpen, TokDisplayClose},
					kinds)
			},
		},
		{
			name: "bracket display",
			src:  `before \[ a = b \] after`,
			check: func(t *testing.T, tokens []Token) {
				var kinds []TokenKind
				for _, tk := range tokens {
					if tk.Kind == TokDisplayOpen || tk.Kind == TokDisplayClose {
						kinds = append(kinds, tk.Kind)
					}
				}
				assert.Equal(t, []TokenKind{TokDisplayOpen, TokDisplayClose}, kinds)
			},
		},
		{
			name: "nested envs order",
			src:  `\begin{proof}\begin{align}x\end{align}\end{proof}`,
			check: func(t *testing.T, tokens []Token) {
				var envs []string
				for _, tk := range tokens {
					switch tk.Kind {
					case TokBeginEnv:
						envs = append(envs, "b:"+tk.EnvName)
					case TokEndEnv:
						envs = append(envs, "e:"+tk.EnvName)
					}
				}
				assert.Equal(t,
					[]string{"b:proof", "b:align", "e:align", "e:proof"},
					envs)
			},
		},
		{
			name: "starred newtheorem",
			src:  `\newtheorem*{rem}{Remark}`,
			check: func(t *testing.T, tokens []Token) {
				var nt *Token
				for i := range tokens {
					if tokens[i].Kind == TokNewTheorem {
						nt = &tokens[i]
					}
				}
				require.NotNil(t, nt)
				assert.True(t, nt.Starred)
				assert.Equal(t, "rem", nt.EnvName)
				assert.Equal(t, "Remark", nt.Title)
				assert.Equal(t, "", nt.Chain)
			},
		},
		{
			name: "chained newtheorem with section counter",
			src:  `\newtheorem{lemma}[theorem]{Lemma}`,
			check: func(t *testing.T, tokens []Token) {
				var nt *Token
				for i := range tokens {
					if tokens[i].Kind == TokNewTheorem {
						nt = &tokens[i]
					}
				}
				require.NotNil(t, nt)
				assert.Equal(t, "theorem", nt.Chain)
				assert.Equal(t, "lemma", nt.EnvName)
				assert.Equal(t, "Lemma", nt.Title)
			},
		},
		{
			name: "newtheorem with counter scope suffix",
			src:  `\newtheorem{thm}{Theorem}[section]`,
			check: func(t *testing.T, tokens []Token) {
				var nt *Token
				for i := range tokens {
					if tokens[i].Kind == TokNewTheorem {
						nt = &tokens[i]
					}
				}
				require.NotNil(t, nt)
				// Trailing [section] is a scope counter, not a "chain"; our
				// tokenizer only records the optional arg that appears BEFORE
				// the title. This documents the MVP behaviour.
				assert.Equal(t, "", nt.Chain)
				assert.Equal(t, "thm", nt.EnvName)
				assert.Equal(t, "Theorem", nt.Title)
			},
		},
		{
			name: "verbatim content is skipped",
			src:  "\\begin{verbatim}\n\\section{fake}\n\\label{fake}\n\\end{verbatim}\n",
			check: func(t *testing.T, tokens []Token) {
				for _, tk := range tokens {
					assert.NotEqual(t, TokSection, tk.Kind, "section inside verbatim must be ignored")
					assert.NotEqual(t, TokLabel, tk.Kind, "label inside verbatim must be ignored")
				}
				var hasBegin, hasEnd bool
				for _, tk := range tokens {
					if tk.Kind == TokBeginEnv && tk.EnvName == "verbatim" {
						hasBegin = true
					}
					if tk.Kind == TokEndEnv && tk.EnvName == "verbatim" {
						hasEnd = true
					}
				}
				assert.True(t, hasBegin)
				assert.True(t, hasEnd)
			},
		},
		{
			name: "comment strips rest of line",
			src:  `hello % \label{fake} \cite{fake}`,
			check: func(t *testing.T, tokens []Token) {
				for _, tk := range tokens {
					assert.NotEqual(t, TokLabel, tk.Kind)
					assert.NotEqual(t, TokRef, tk.Kind)
				}
			},
		},
		{
			name: "escaped percent does not start comment",
			src:  `rate is 50\% \label{pct}`,
			check: func(t *testing.T, tokens []Token) {
				var found bool
				for _, tk := range tokens {
					if tk.Kind == TokLabel && tk.Target == "pct" {
						found = true
					}
				}
				assert.True(t, found, "\\label after \\%% should be tokenized")
			},
		},
		{
			name: "full-line comment",
			src:  "% just a comment\n\\section{Intro}\n",
			check: func(t *testing.T, tokens []Token) {
				var gotComment, gotSection bool
				for _, tk := range tokens {
					if tk.Kind == TokCommentLine {
						gotComment = true
					}
					if tk.Kind == TokSection {
						gotSection = true
					}
				}
				assert.True(t, gotComment)
				assert.True(t, gotSection)
			},
		},
		{
			name: "blank line token",
			src:  "\\section{A}\n\n\\section{B}\n",
			check: func(t *testing.T, tokens []Token) {
				var blanks int
				for _, tk := range tokens {
					if tk.Kind == TokBlankLine {
						blanks++
					}
				}
				assert.Equal(t, 1, blanks)
			},
		},
		{
			name: "line numbers are 1-based",
			src:  "line1\n\\label{a}\n",
			check: func(t *testing.T, tokens []Token) {
				var lbl *Token
				for i := range tokens {
					if tokens[i].Kind == TokLabel {
						lbl = &tokens[i]
					}
				}
				require.NotNil(t, lbl)
				assert.Equal(t, 2, lbl.Line)
			},
		},
		{
			name: "ref with nested braces in arg",
			src:  `\ref{eq:{nested}}`,
			check: func(t *testing.T, tokens []Token) {
				var targets []string
				for _, tk := range tokens {
					if tk.Kind == TokRef {
						targets = append(targets, tk.Target)
					}
				}
				assert.Equal(t, []string{"eq:{nested}"}, targets)
			},
		},
		{
			name: "theoremstyle captured",
			src:  `\theoremstyle{remark}`,
			check: func(t *testing.T, tokens []Token) {
				var ts *Token
				for i := range tokens {
					if tokens[i].Kind == TokTheoremStyle {
						ts = &tokens[i]
					}
				}
				require.NotNil(t, ts)
				assert.Equal(t, "remark", ts.EnvName)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tokens := Tokenize([]byte(tc.src))
			tc.check(t, tokens)
		})
	}
}

func TestTokenKindString(t *testing.T) {
	// smoke test: every named kind returns a non-empty string.
	for k := TokBeginEnv; k <= TokItem; k++ {
		assert.NotEmpty(t, k.String(), "kind %d has empty string", int(k))
	}
	assert.Equal(t, "Unknown", TokenKind(999).String())
}

func TestTokenize_Item(t *testing.T) {
	cases := []struct {
		name      string
		src       string
		wantCount int
		wantTitle string // title of the first TokItem; "" if no optional arg
	}{
		{
			name:      "bare item",
			src:       "\\begin{itemize}\n\\item one\n\\item two\n\\end{itemize}\n",
			wantCount: 2,
			wantTitle: "",
		},
		{
			name:      "item with bracket label",
			src:       "\\begin{description}\n\\item[Lemma] foo\n\\end{description}\n",
			wantCount: 1,
			wantTitle: "Lemma",
		},
		{
			name:      "item with nested brackets in label",
			src:       "\\begin{description}\n\\item[$[a,b]$] interval\n\\end{description}\n",
			wantCount: 1,
			wantTitle: "$[a,b]$",
		},
		{
			name:      "multiple items on one line",
			src:       "\\begin{itemize}\n\\item one \\item two \\item three\n\\end{itemize}\n",
			wantCount: 3,
			wantTitle: "",
		},
		{
			name:      "itemsep is not item",
			src:       "\\setlength{\\itemsep}{0pt}\n\\begin{itemize}\n\\item one\n\\end{itemize}\n",
			wantCount: 1,
			wantTitle: "",
		},
		{
			name:      "item inside verbatim is skipped",
			src:       "\\begin{verbatim}\n\\item not a token\n\\end{verbatim}\n",
			wantCount: 0,
			wantTitle: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tokens := Tokenize([]byte(tc.src))
			var items []Token
			for _, tk := range tokens {
				if tk.Kind == TokItem {
					items = append(items, tk)
				}
			}
			assert.Equal(t, tc.wantCount, len(items))
			if tc.wantCount > 0 {
				assert.Equal(t, tc.wantTitle, items[0].Title)
			}
		})
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	// Tests run from the package directory; testdata lives two levels up.
	path := filepath.Join("..", "..", "testdata", name)
	b, err := os.ReadFile(path)
	require.NoError(t, err, "read %s", path)
	return b
}

// Small compile-time / lint sanity: use fmt to format debug output when a
// future assertion needs richer context. Referencing fmt keeps the import
// stable across edits.
var _ = fmt.Sprintf
