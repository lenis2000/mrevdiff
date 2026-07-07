package format

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- ParseLineRange tests ---

func TestParseLineRange_Valid(t *testing.T) {
	tests := []struct {
		input string
		want  [2]int
	}{
		{"1:10", [2]int{1, 10}},
		{"42:120", [2]int{42, 120}},
		{"5:5", [2]int{5, 5}},     // single line
		{" 3 : 7 ", [2]int{3, 7}}, // whitespace
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseLineRange(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseLineRange_Invalid(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"no colon", "1-10"},
		{"empty start", ":10"},
		{"empty end", "1:"},
		{"non-numeric start", "abc:10"},
		{"non-numeric end", "1:xyz"},
		{"start < 1", "0:10"},
		{"end < start", "10:5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseLineRange(tt.input)
			assert.Error(t, err)
		})
	}
}

// --- ClipToRange tests ---

func TestClipToRange_InRangeOnly(t *testing.T) {
	// 5 lines; format changes lines 2 and 4 (trailing whitespace stripped).
	original := []byte("line1\nline2  \nline3\nline4  \nline5\n")
	formatted := []byte("line1\nline2\nline3\nline4\nline5\n")

	// Range 2:4 -> lines 2,3,4 come from formatted; lines 1,5 from original.
	got, err := ClipToRange(original, formatted, [2]int{2, 4})
	require.NoError(t, err)
	assert.Equal(t, "line1\nline2\nline3\nline4\nline5\n", string(got))
}

func TestClipToRange_OutOfRangePreserved(t *testing.T) {
	// Same input but range is only line 1 (no changes on line 1).
	original := []byte("line1\nline2  \nline3\n")
	formatted := []byte("line1\nline2\nline3\n")

	// Range 1:1 -> only line 1 from formatted (same); lines 2,3 from original.
	got, err := ClipToRange(original, formatted, [2]int{1, 1})
	require.NoError(t, err)
	// Line 2 should keep its trailing spaces since it's out of range.
	assert.Equal(t, "line1\nline2  \nline3\n", string(got))
}

func TestClipToRange_FullRange(t *testing.T) {
	original := []byte("a  \nb  \nc  \n")
	formatted := []byte("a\nb\nc\n")

	got, err := ClipToRange(original, formatted, [2]int{1, 3})
	require.NoError(t, err)
	assert.Equal(t, "a\nb\nc\n", string(got))
}

func TestClipToRange_RangePastEOF(t *testing.T) {
	original := []byte("a\nb\n")
	formatted := []byte("a\nb\n")

	// Range extends past the file.
	got, err := ClipToRange(original, formatted, [2]int{1, 100})
	require.NoError(t, err)
	assert.Equal(t, "a\nb\n", string(got))
}

func TestClipToRange_RangeEntirelyPastEOF(t *testing.T) {
	original := []byte("a\nb\n")
	formatted := []byte("a\nb\n")

	// Range starts past EOF.
	got, err := ClipToRange(original, formatted, [2]int{50, 100})
	require.NoError(t, err)
	assert.Equal(t, "a\nb\n", string(got))
}

func TestClipToRange_LineMismatchError(t *testing.T) {
	// Different line counts => error.
	original := []byte("a\nb\n")
	formatted := []byte("a\nb\nc\n")

	_, err := ClipToRange(original, formatted, [2]int{1, 1})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "line count mismatch")
}

func TestClipToRange_SingleLine(t *testing.T) {
	original := []byte("hello  \n")
	formatted := []byte("hello\n")

	got, err := ClipToRange(original, formatted, [2]int{1, 1})
	require.NoError(t, err)
	assert.Equal(t, "hello\n", string(got))
}

// --- splitKeepNL tests ---

func TestSplitKeepNL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"single newline", "\n", []string{"\n"}},
		{"line with trailing newline", "abc\n", []string{"abc\n"}},
		{"two lines", "abc\ndef\n", []string{"abc\n", "def\n"}},
		{"no trailing newline", "abc\ndef", []string{"abc\n", "def"}},
		{"three lines", "a\nb\nc\n", []string{"a\n", "b\n", "c\n"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitKeepNL([]byte(tt.in))
			var gotStr []string
			for _, b := range got {
				gotStr = append(gotStr, string(b))
			}
			assert.Equal(t, tt.want, gotStr)
		})
	}
}

// --- Integration: --lines disables line-count-changing rules ---

func TestLineRange_DisablesLineChangingRules(t *testing.T) {
	lr := [2]int{1, 10}
	opts := Options{
		PDFFix:    true,
		LineRange: &lr,
	}
	enabled := enabledRules(opts)
	for _, r := range enabled {
		assert.False(t, lineCountChangingRules[r.ID],
			"rule %s should be disabled under --lines", r.ID)
	}
}

func TestLineRange_SkippedRulesReported(t *testing.T) {
	lr := [2]int{1, 10}
	opts := Options{
		PDFFix:    true,
		LineRange: &lr,
	}
	skipped := SkippedLineRangeRules(opts)
	// Safe rules always on that change line counts:
	assert.Contains(t, skipped, "space.blank-runs")
	assert.Contains(t, skipped, "space.wrap")
	assert.Contains(t, skipped, "space.item-per-line")
	assert.Contains(t, skipped, "space.proof-delim-per-line")
	assert.Contains(t, skipped, "space.display-delim-per-line")
	assert.Contains(t, skipped, "display.style")
	// PDFFix rules (enabled via opts.PDFFix): math.paragraph-suppress, env.spacing
	assert.Contains(t, skipped, "math.paragraph-suppress")
	assert.Contains(t, skipped, "env.spacing")
}

func TestLineRange_NoSkipWhenNil(t *testing.T) {
	opts := Options{PDFFix: true}
	skipped := SkippedLineRangeRules(opts)
	assert.Empty(t, skipped)
}

// --- End-to-end: Apply with LineRange + ClipToRange ---

func TestApplyWithLineRange_OnlyInRangeChanges(t *testing.T) {
	// Input has trailing whitespace on lines 2 and 4.
	input := "\\documentclass{amsart}\n\\begin{document}\nhi  \nbye  \n\\end{document}\n"
	lr := [2]int{3, 3} // only format line 3
	opts := Options{
		Rules:     []string{"space.trailing"},
		LineRange: &lr,
	}
	result := Apply([]byte(input), opts)
	// The full pipeline strips trailing whitespace from all lines.
	// ClipToRange restricts to line 3 only.
	clipped, err := ClipToRange([]byte(input), result.Src, *opts.LineRange)
	require.NoError(t, err)
	// Line 3 should be "hi\n" (trimmed), line 4 should keep "bye  \n" (original).
	assert.Contains(t, string(clipped), "hi\n")
	assert.Contains(t, string(clipped), "bye  \n")
}
