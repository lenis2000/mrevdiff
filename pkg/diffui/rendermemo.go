package diffui

import (
	"strings"

	"github.com/lenis2000/mrevdiff/pkg/diffreview"
	"github.com/lenis2000/mrevdiff/pkg/parser"
)

// Bubble Tea re-renders the full frame on every message, and the outline
// and both source panes re-derive their rows from the same per-pair token
// diff + quadratic line alignment — the dominant per-keystroke cost on a
// large review. The memos below cache those pure computations. Update and
// View run on one goroutine, so no locking. Keys hold the block pointers
// themselves, which keeps the blocks alive for exactly as long as their
// entry exists — a key can therefore never alias a different block. The
// memos are reset whenever the review is replaced; the size cap catches
// synthetic coalesced blocks, which are re-created on every call and
// would otherwise grow the map per keystroke.

type rowsMemoKey struct {
	oldBlock *parser.Block
	newBlock *parser.Block
	status   diffreview.PairStatus
}

const rowsMemoCap = 8192

var rowsMemo = map[rowsMemoKey][]sourceRow{}

// memoizedSourceRows is the caching entry point for sourceRows. Callers
// must not mutate the returned slice.
func memoizedSourceRows(pair *diffreview.Pair) []sourceRow {
	if pair == nil {
		return sourceRows(pair)
	}
	key := rowsMemoKey{oldBlock: pair.Old, newBlock: pair.New, status: pair.Status}
	if rows, ok := rowsMemo[key]; ok {
		return rows
	}
	rows := sourceRows(pair)
	if len(rowsMemo) >= rowsMemoCap {
		clear(rowsMemo)
	}
	rowsMemo[key] = rows
	return rows
}

// fileLinesMemo caches whole-file line splits for the coalesced regime's
// synthetic blocks, keyed by the source's backing array. Tiny cap: at most
// the two sides of a couple of live reviews.
var fileLinesMemo = map[*byte][]string{}

func memoizedSplitLines(source []byte) []string {
	if len(source) == 0 {
		return nil
	}
	key := &source[0]
	if lines, ok := fileLinesMemo[key]; ok {
		return lines
	}
	lines := strings.Split(string(source), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(fileLinesMemo) >= 8 {
		clear(fileLinesMemo)
	}
	fileLinesMemo[key] = lines
	return lines
}

func resetRenderMemos() {
	clear(rowsMemo)
	clear(fileLinesMemo)
}
