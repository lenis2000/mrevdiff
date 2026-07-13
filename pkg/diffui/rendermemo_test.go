package diffui

import (
	"reflect"
	"testing"

	"github.com/lenis2000/mrevdiff/pkg/diffreview"
)

func TestMemoizedSourceRowsReturnsCachedSlice(t *testing.T) {
	resetRenderMemos()
	review := fixtureReview()
	var pair *diffreview.Pair
	for i := range review.Pairs {
		if review.Pairs[i].Status == diffreview.Changed {
			pair = &review.Pairs[i]
			break
		}
	}
	if pair == nil {
		t.Fatalf("fixture has no changed pair")
	}
	first := memoizedSourceRows(pair)
	second := memoizedSourceRows(pair)
	if len(first) == 0 {
		t.Fatalf("expected rows for changed pair")
	}
	if &first[0] != &second[0] {
		t.Fatalf("second call should return the cached slice")
	}
	if !reflect.DeepEqual(first, sourceRows(pair)) {
		t.Fatalf("memoized rows differ from direct computation")
	}

	resetRenderMemos()
	third := memoizedSourceRows(pair)
	if &first[0] == &third[0] {
		t.Fatalf("reset should drop the cached slice")
	}
}

func TestMemoizedSplitLinesMatchesSourceLineRange(t *testing.T) {
	resetRenderMemos()
	src := []byte("alpha\nbeta\ngamma\n")
	if got := sourceLineRange(src, 2, 3); got != "beta\ngamma" {
		t.Fatalf("sourceLineRange = %q", got)
	}
	// Second call hits the memo and must agree.
	if got := sourceLineRange(src, 1, 1); got != "alpha" {
		t.Fatalf("memoized sourceLineRange = %q", got)
	}
	if got := sourceLineRange(src, 4, 5); got != "" {
		t.Fatalf("out-of-range should stay empty, got %q", got)
	}
}
