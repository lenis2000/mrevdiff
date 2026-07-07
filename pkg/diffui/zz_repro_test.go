package diffui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/lenis2000/mrevdiff/pkg/diffreview"
)

var ansiStrip = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripANSI(s string) string { return ansiStrip.ReplaceAllString(s, "") }

func TestReproMissingChars(t *testing.T) {
	oldText := "= exact transition, \\texttt{tr} = exact layered permutations; verified against them for $n\\le 17$ in \\Cref{tab:layered_benchmark});"
	newText := "= exact transition, \\texttt{prod} = exact layered product formula (the recurrences are infeasible on these near-maximal layered permutations; cross-checked against them for $n\\le 17$ in \\Cref{tab:layered_benchmark});"
	pair := &diffreview.Pair{
		Status: diffreview.Changed,
		Old:    fixtureBlock("old", 25, oldText),
		New:    fixtureBlock("new", 25, newText),
	}

	for _, width := range []int{58, 60, 61} {
		body := RenderPairSourceSideHighlighted(pair, false, width, 40, 0, 25)
		var visibleContent strings.Builder
		for _, line := range strings.Split(body, "\n") {
			plain := stripANSI(line)
			if w := len([]rune(plain)); w != width {
				t.Logf("width=%d: row visible width %d != %d: %q", width, w, width, plain)
			}
			// strip the 7-col prefix and trailing padding to recover content
			r := []rune(plain)
			if len(r) > 7 {
				content := strings.TrimRight(string(r[7:]), " ")
				visibleContent.WriteString(content)
			}
		}
		got := stripSpaces(visibleContent.String())
		want := stripSpaces(newText)
		if !strings.Contains(got, stripSpaces("layered product formula")) {
			t.Errorf("width=%d: LOST characters — 'layered product formula' not intact.\n  reconstructed=%q", width, visibleContent.String())
		}
		_ = got
		_ = want
	}
}

func stripSpaces(s string) string { return strings.ReplaceAll(s, " ", "") }
