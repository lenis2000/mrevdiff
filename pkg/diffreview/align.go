package diffreview

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/lenis2000/mrevdiff/pkg/parser"
)

// PairStatus describes how one semantic block changed between endpoints.
type PairStatus int

const (
	Unchanged PairStatus = iota
	FormatOnly
	Changed
	Added
	Deleted
	Moved
)

func (s PairStatus) String() string {
	switch s {
	case Unchanged:
		return "unchanged"
	case FormatOnly:
		return "format-only"
	case Changed:
		return "changed"
	case Added:
		return "added"
	case Deleted:
		return "deleted"
	case Moved:
		return "moved"
	default:
		return fmt.Sprintf("PairStatus(%d)", int(s))
	}
}

// Pair is an aligned old/new semantic block.
type Pair struct {
	ID             string
	Status         PairStatus
	Old            *parser.Block
	New            *parser.Block
	Score          float64
	OldIndex       int
	NewIndex       int
	SectionPathOld []string
	SectionPathNew []string
}

// DiffStats summarizes the pair statuses in a Review.
type DiffStats struct {
	Total      int
	Unchanged  int
	FormatOnly int
	Changed    int
	Added      int
	Deleted    int
	Moved      int
}

// Review is the semantic before/after diff for two endpoints.
type Review struct {
	Old    Endpoint
	New    Endpoint
	OldDoc *parser.Document
	NewDoc *parser.Document
	Pairs  []Pair
	ByID   map[string]*Pair
	Stats  DiffStats
}

// BuildReview parses both endpoints and aligns their semantic blocks.
func BuildReview(oldEndpoint, newEndpoint Endpoint) (*Review, error) {
	oldDoc, err := parser.Parse(oldEndpoint.Source)
	if err != nil {
		return nil, fmt.Errorf("parse old endpoint: %w", err)
	}
	newDoc, err := parser.Parse(newEndpoint.Source)
	if err != nil {
		return nil, fmt.Errorf("parse new endpoint: %w", err)
	}
	applyEndpointFile(oldDoc, oldEndpoint)
	applyEndpointFile(newDoc, newEndpoint)
	return AlignDocuments(oldEndpoint, newEndpoint, oldDoc, newDoc), nil
}

// RebuildWithOldDoc re-aligns after a new-side edit, reusing the old
// endpoint's already parsed document — only the new file's bytes can
// have changed, so re-parsing the old side is pure waste on every edit.
func RebuildWithOldDoc(oldDoc *parser.Document, oldEndpoint, newEndpoint Endpoint) (*Review, error) {
	if oldDoc == nil {
		return BuildReview(oldEndpoint, newEndpoint)
	}
	newDoc, err := parser.Parse(newEndpoint.Source)
	if err != nil {
		return nil, fmt.Errorf("parse new endpoint: %w", err)
	}
	applyEndpointFile(newDoc, newEndpoint)
	return AlignDocuments(oldEndpoint, newEndpoint, oldDoc, newDoc), nil
}

// AlignDocuments aligns two already parsed documents.
func AlignDocuments(oldEndpoint, newEndpoint Endpoint, oldDoc, newDoc *parser.Document) *Review {
	oldBlocks := reviewBlocks(oldDoc)
	newBlocks := reviewBlocks(newDoc)
	oldMeta := buildBlockMeta(oldDoc, oldBlocks)
	newMeta := buildBlockMeta(newDoc, newBlocks)

	oldMatched := make([]bool, len(oldBlocks))
	newMatched := make([]bool, len(newBlocks))
	matches := make([]blockMatch, 0, min(len(oldBlocks), len(newBlocks)))
	oldToNew := map[int]int{}

	addMatch := func(oldIndex, newIndex int, score float64, strong bool) {
		oldMatched[oldIndex] = true
		newMatched[newIndex] = true
		oldToNew[oldIndex] = newIndex
		matches = append(matches, blockMatch{
			OldIndex: oldIndex,
			NewIndex: newIndex,
			Score:    score,
			Strong:   strong,
		})
	}

	matchByLabel(oldBlocks, newBlocks, oldMatched, newMatched, addMatch)
	matchByStableID(oldBlocks, newBlocks, oldMeta, newMeta, oldMatched, newMatched, addMatch)
	matchProofsAfterMatchedTheorems(oldDoc, newDoc, oldBlocks, newBlocks, oldMatched, newMatched, oldToNew, addMatch)
	matchByNormalizedHash(oldBlocks, newBlocks, oldMeta, newMeta, oldMatched, newMatched, addMatch)
	matchByFuzzyScore(oldBlocks, newBlocks, oldMeta, newMeta, oldMatched, newMatched, addMatch)

	pairs := orderPairs(oldBlocks, newBlocks, oldMeta, newMeta, oldMatched, newMatched, matches)
	ensureUniquePairIDs(pairs)
	review := &Review{
		Old:    oldEndpoint,
		New:    newEndpoint,
		OldDoc: oldDoc,
		NewDoc: newDoc,
		Pairs:  pairs,
		ByID:   map[string]*Pair{},
	}
	for i := range review.Pairs {
		review.ByID[review.Pairs[i].ID] = &review.Pairs[i]
		review.Stats.add(review.Pairs[i].Status)
	}
	return review
}

type blockMeta struct {
	Index       int
	Normalized  string
	NormHash    string
	Tokens      []string
	SectionPath []string
}

type blockMatch struct {
	OldIndex int
	NewIndex int
	Score    float64
	Strong   bool
}

func reviewBlocks(doc *parser.Document) []*parser.Block {
	if doc == nil || doc.Root == nil {
		return nil
	}
	out := make([]*parser.Block, 0, len(doc.Blocks))
	var walk func(*parser.Block)
	walk = func(b *parser.Block) {
		if b == nil || b == doc.Root || b.ID == "root" {
			for _, child := range sortedChildren(doc, b) {
				walk(child)
			}
			return
		}
		if b.Kind == parser.KindSection {
			for _, child := range sortedChildren(doc, b) {
				walk(child)
			}
			return
		}
		if reviewBlockIsAtomic(b) || len(b.ChildIDs) == 0 {
			out = append(out, b)
			return
		}
		for _, child := range sortedChildren(doc, b) {
			walk(child)
		}
	}
	walk(doc.Root)
	return out
}

func reviewBlockIsAtomic(b *parser.Block) bool {
	if b == nil {
		return false
	}
	switch b.Kind {
	case parser.KindTheoremLike, parser.KindProof, parser.KindDisplay, parser.KindFigure, parser.KindAbstract, parser.KindBibliography, parser.KindProofStep:
		return true
	default:
		return false
	}
}

func sortedChildren(doc *parser.Document, b *parser.Block) []*parser.Block {
	if doc == nil || b == nil || len(b.ChildIDs) == 0 {
		return nil
	}
	children := make([]*parser.Block, 0, len(b.ChildIDs))
	for _, id := range b.ChildIDs {
		if child := doc.ByID[id]; child != nil {
			children = append(children, child)
		}
	}
	sort.SliceStable(children, func(i, j int) bool {
		if children[i].StartLine != children[j].StartLine {
			return children[i].StartLine < children[j].StartLine
		}
		return children[i].EndLine < children[j].EndLine
	})
	return children
}

func buildBlockMeta(doc *parser.Document, blocks []*parser.Block) []blockMeta {
	out := make([]blockMeta, len(blocks))
	for i, b := range blocks {
		normalized := NormalizeSourceForMatch(b.Source)
		out[i] = blockMeta{
			Index:       i,
			Normalized:  normalized,
			NormHash:    hashString(normalized),
			Tokens:      matchTokens(normalized),
			SectionPath: sectionPath(doc, b),
		}
	}
	return out
}

func matchByLabel(oldBlocks, newBlocks []*parser.Block, oldMatched, newMatched []bool, addMatch func(int, int, float64, bool)) {
	oldByLabel := uniqueStringIndex(oldBlocks, func(b *parser.Block) string { return b.Label })
	for newIndex, newBlock := range newBlocks {
		if newMatched[newIndex] || newBlock.Label == "" {
			continue
		}
		oldIndex, ok := oldByLabel[newBlock.Label]
		if !ok || oldMatched[oldIndex] || !compatibleKind(oldBlocks[oldIndex], newBlock) {
			continue
		}
		addMatch(oldIndex, newIndex, 1, true)
	}
}

func matchByStableID(
	oldBlocks, newBlocks []*parser.Block,
	oldMeta, newMeta []blockMeta,
	oldMatched, newMatched []bool,
	addMatch func(int, int, float64, bool),
) {
	oldByID := uniqueStringIndex(oldBlocks, func(b *parser.Block) string { return b.ID })
	for newIndex, newBlock := range newBlocks {
		if newMatched[newIndex] || newBlock.ID == "" {
			continue
		}
		oldIndex, ok := oldByID[newBlock.ID]
		if !ok || oldMatched[oldIndex] || !compatibleKind(oldBlocks[oldIndex], newBlock) {
			continue
		}
		if duplicatedGenericProse(oldBlocks[oldIndex], oldBlocks, oldMeta, oldIndex) ||
			duplicatedGenericProse(newBlock, newBlocks, newMeta, newIndex) {
			continue
		}
		addMatch(oldIndex, newIndex, 1, true)
	}
}

func matchProofsAfterMatchedTheorems(
	oldDoc, newDoc *parser.Document,
	oldBlocks, newBlocks []*parser.Block,
	oldMatched, newMatched []bool,
	oldToNew map[int]int,
	addMatch func(int, int, float64, bool),
) {
	oldIndexByBlock := indexByBlock(oldBlocks)
	newIndexByBlock := indexByBlock(newBlocks)
	for newIndex, newBlock := range newBlocks {
		if newMatched[newIndex] || newBlock.Kind != parser.KindProof {
			continue
		}
		newPrev := previousSibling(newDoc, newBlock)
		if newPrev == nil || newPrev.Kind != parser.KindTheoremLike {
			continue
		}
		newPrevIndex, ok := newIndexByBlock[newPrev]
		if !ok {
			continue
		}
		for oldIndex, oldBlock := range oldBlocks {
			if oldMatched[oldIndex] || oldBlock.Kind != parser.KindProof {
				continue
			}
			oldPrev := previousSibling(oldDoc, oldBlock)
			if oldPrev == nil || oldPrev.Kind != parser.KindTheoremLike {
				continue
			}
			oldPrevIndex, ok := oldIndexByBlock[oldPrev]
			if !ok {
				continue
			}
			if matchedNewIndex, ok := oldToNew[oldPrevIndex]; ok && matchedNewIndex == newPrevIndex {
				addMatch(oldIndex, newIndex, 0.99, true)
				break
			}
		}
	}
}

func matchByNormalizedHash(
	oldBlocks, newBlocks []*parser.Block,
	oldMeta, newMeta []blockMeta,
	oldMatched, newMatched []bool,
	addMatch func(int, int, float64, bool),
) {
	type groupKey struct {
		kind parser.Kind
		hash string
	}
	oldGroups := map[groupKey][]int{}
	newGroups := map[groupKey][]int{}
	for i, b := range oldBlocks {
		if oldMatched[i] || oldMeta[i].Normalized == "" {
			continue
		}
		key := groupKey{kind: b.Kind, hash: oldMeta[i].NormHash}
		oldGroups[key] = append(oldGroups[key], i)
	}
	for i, b := range newBlocks {
		if newMatched[i] || newMeta[i].Normalized == "" {
			continue
		}
		key := groupKey{kind: b.Kind, hash: newMeta[i].NormHash}
		newGroups[key] = append(newGroups[key], i)
	}

	var keys []groupKey
	for key := range newGroups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].kind != keys[j].kind {
			return keys[i].kind < keys[j].kind
		}
		return keys[i].hash < keys[j].hash
	})

	for _, key := range keys {
		oldList := oldGroups[key]
		newList := newGroups[key]
		if len(oldList) == 1 && len(newList) == 1 {
			oldIndex, newIndex := oldList[0], newList[0]
			if !oldMatched[oldIndex] && !newMatched[newIndex] && nearSection(oldMeta[oldIndex].SectionPath, newMeta[newIndex].SectionPath) {
				addMatch(oldIndex, newIndex, 0.98, false)
			}
			continue
		}
		matchUniqueSectionPath(oldList, newList, oldMeta, newMeta, oldMatched, newMatched, addMatch)
	}
}

func matchUniqueSectionPath(
	oldList, newList []int,
	oldMeta, newMeta []blockMeta,
	oldMatched, newMatched []bool,
	addMatch func(int, int, float64, bool),
) {
	oldByPath := map[string][]int{}
	newByPath := map[string][]int{}
	for _, oldIndex := range oldList {
		path := sectionPathKey(oldMeta[oldIndex].SectionPath)
		oldByPath[path] = append(oldByPath[path], oldIndex)
	}
	for _, newIndex := range newList {
		path := sectionPathKey(newMeta[newIndex].SectionPath)
		newByPath[path] = append(newByPath[path], newIndex)
	}
	var paths []string
	for path := range newByPath {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		oldAtPath := oldByPath[path]
		newAtPath := newByPath[path]
		if len(oldAtPath) != 1 || len(newAtPath) != 1 {
			continue
		}
		oldIndex, newIndex := oldAtPath[0], newAtPath[0]
		if !oldMatched[oldIndex] && !newMatched[newIndex] {
			addMatch(oldIndex, newIndex, 0.98, false)
		}
	}
}

func matchByFuzzyScore(
	oldBlocks, newBlocks []*parser.Block,
	oldMeta, newMeta []blockMeta,
	oldMatched, newMatched []bool,
	addMatch func(int, int, float64, bool),
) {
	var candidates []blockMatch
	for newIndex, newBlock := range newBlocks {
		if newMatched[newIndex] || newMeta[newIndex].Normalized == "" {
			continue
		}
		best := blockMatch{NewIndex: newIndex, Score: -1}
		secondScore := -1.0
		for oldIndex, oldBlock := range oldBlocks {
			if oldMatched[oldIndex] || oldMeta[oldIndex].Normalized == "" || !compatibleKind(oldBlock, newBlock) {
				continue
			}
			if !fuzzyEligible(oldBlock, oldMeta[oldIndex]) || !fuzzyEligible(newBlock, newMeta[newIndex]) {
				continue
			}
			if !nearSection(oldMeta[oldIndex].SectionPath, newMeta[newIndex].SectionPath) {
				continue
			}
			score := fuzzyScore(oldBlock, newBlock, oldMeta[oldIndex], newMeta[newIndex])
			if score > best.Score {
				secondScore = best.Score
				best = blockMatch{OldIndex: oldIndex, NewIndex: newIndex, Score: score}
			} else if score > secondScore {
				secondScore = score
			}
		}
		if best.Score < fuzzyThreshold(newBlock) {
			continue
		}
		if secondScore >= 0 && best.Score-secondScore < 0.04 {
			continue
		}
		candidates = append(candidates, best)
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		if candidates[i].NewIndex != candidates[j].NewIndex {
			return candidates[i].NewIndex < candidates[j].NewIndex
		}
		return candidates[i].OldIndex < candidates[j].OldIndex
	})
	for _, c := range candidates {
		if oldMatched[c.OldIndex] || newMatched[c.NewIndex] {
			continue
		}
		addMatch(c.OldIndex, c.NewIndex, c.Score, false)
	}
}

func orderPairs(
	oldBlocks, newBlocks []*parser.Block,
	oldMeta, newMeta []blockMeta,
	oldMatched, newMatched []bool,
	matches []blockMatch,
) []Pair {
	matchByNew := map[int]blockMatch{}
	for _, m := range matches {
		matchByNew[m.NewIndex] = m
	}

	var unmatchedOld []int
	for i := range oldBlocks {
		if !oldMatched[i] {
			unmatchedOld = append(unmatchedOld, i)
		}
	}
	insertedDeleted := make(map[int]bool, len(unmatchedOld))
	pairs := make([]Pair, 0, len(newBlocks)+len(unmatchedOld))
	lastMatchedOld := -1

	insertDeletedBetween := func(low, high int) {
		if high <= low {
			return
		}
		for _, oldIndex := range unmatchedOld {
			if insertedDeleted[oldIndex] {
				continue
			}
			if oldIndex > low && oldIndex < high {
				pairs = append(pairs, makeDeletedPair(oldBlocks, oldMeta, oldIndex))
				insertedDeleted[oldIndex] = true
			}
		}
	}

	for newIndex := range newBlocks {
		if m, ok := matchByNew[newIndex]; ok {
			insertDeletedBetween(lastMatchedOld, m.OldIndex)
			pairs = append(pairs, makeMatchedPair(oldBlocks, newBlocks, oldMeta, newMeta, m))
			if m.OldIndex > lastMatchedOld {
				lastMatchedOld = m.OldIndex
			}
			continue
		}
		if !newMatched[newIndex] {
			pairs = append(pairs, makeAddedPair(newBlocks, newMeta, newIndex))
		}
	}
	for _, oldIndex := range unmatchedOld {
		if insertedDeleted[oldIndex] {
			continue
		}
		pairs = append(pairs, makeDeletedPair(oldBlocks, oldMeta, oldIndex))
	}
	return pairs
}

func makeMatchedPair(oldBlocks, newBlocks []*parser.Block, oldMeta, newMeta []blockMeta, m blockMatch) Pair {
	oldBlock := oldBlocks[m.OldIndex]
	newBlock := newBlocks[m.NewIndex]
	status := statusFor(oldBlock, newBlock, oldMeta[m.OldIndex], newMeta[m.NewIndex], m.Strong)
	return Pair{
		ID:             matchedPairID(oldBlock, newBlock),
		Status:         status,
		Old:            oldBlock,
		New:            newBlock,
		Score:          m.Score,
		OldIndex:       m.OldIndex,
		NewIndex:       m.NewIndex,
		SectionPathOld: append([]string(nil), oldMeta[m.OldIndex].SectionPath...),
		SectionPathNew: append([]string(nil), newMeta[m.NewIndex].SectionPath...),
	}
}

func makeAddedPair(newBlocks []*parser.Block, newMeta []blockMeta, newIndex int) Pair {
	newBlock := newBlocks[newIndex]
	return Pair{
		ID:             pairIDForNew(newBlock),
		Status:         Added,
		New:            newBlock,
		Score:          0,
		OldIndex:       -1,
		NewIndex:       newIndex,
		SectionPathNew: append([]string(nil), newMeta[newIndex].SectionPath...),
	}
}

func makeDeletedPair(oldBlocks []*parser.Block, oldMeta []blockMeta, oldIndex int) Pair {
	oldBlock := oldBlocks[oldIndex]
	return Pair{
		ID:             pairIDForDeleted(oldBlock),
		Status:         Deleted,
		Old:            oldBlock,
		Score:          0,
		OldIndex:       oldIndex,
		NewIndex:       -1,
		SectionPathOld: append([]string(nil), oldMeta[oldIndex].SectionPath...),
	}
}

func statusFor(oldBlock, newBlock *parser.Block, oldMeta, newMeta blockMeta, strong bool) PairStatus {
	if strong && !sameStringSlice(oldMeta.SectionPath, newMeta.SectionPath) {
		return Moved
	}
	if oldBlock.Source == newBlock.Source {
		return Unchanged
	}
	if oldBlock.Label != newBlock.Label {
		return Changed
	}
	if oldMeta.Normalized == newMeta.Normalized {
		return FormatOnly
	}
	return Changed
}

func matchedPairID(oldBlock, newBlock *parser.Block) string {
	if newBlock != nil && newBlock.Label != "" {
		if newBlock.ID != "" {
			return newBlock.ID
		}
		return newBlock.Label
	}
	if oldBlock != nil && oldBlock.Label != "" {
		if oldBlock.ID != "" {
			return oldBlock.ID
		}
		return oldBlock.Label
	}
	if oldBlock != nil && oldBlock.ID != "" {
		return oldBlock.ID
	}
	if newBlock != nil && newBlock.ID != "" {
		return newBlock.ID
	}
	oldLine, newLine := 0, 0
	if oldBlock != nil {
		oldLine = oldBlock.StartLine
	}
	if newBlock != nil {
		newLine = newBlock.StartLine
	}
	return fmt.Sprintf("old:%d-new:%d", oldLine, newLine)
}

func pairIDForNew(block *parser.Block) string {
	if block == nil {
		return ""
	}
	if block.ID != "" {
		return block.ID
	}
	if block.Label != "" {
		return block.Label
	}
	return fmt.Sprintf("new:%d", block.StartLine)
}

func ensureUniquePairIDs(pairs []Pair) {
	seen := map[string]int{}
	for i := range pairs {
		id := pairs[i].ID
		if id == "" {
			id = fmt.Sprintf("pair:%d", i+1)
		}
		seen[id]++
		if seen[id] == 1 {
			pairs[i].ID = id
			continue
		}
		for n := seen[id]; ; n++ {
			cand := fmt.Sprintf("%s~%d", id, n)
			if seen[cand] == 0 {
				seen[id] = n
				seen[cand] = 1
				pairs[i].ID = cand
				break
			}
		}
	}
}

func pairIDForDeleted(block *parser.Block) string {
	if block == nil {
		return ""
	}
	if block.ID != "" {
		return "old:" + block.ID
	}
	return fmt.Sprintf("old:%d", block.StartLine)
}

func (s *DiffStats) add(status PairStatus) {
	s.Total++
	switch status {
	case Unchanged:
		s.Unchanged++
	case FormatOnly:
		s.FormatOnly++
	case Changed:
		s.Changed++
	case Added:
		s.Added++
	case Deleted:
		s.Deleted++
	case Moved:
		s.Moved++
	}
}

func uniqueStringIndex(blocks []*parser.Block, value func(*parser.Block) string) map[string]int {
	out := map[string]int{}
	duplicate := map[string]bool{}
	for i, b := range blocks {
		v := value(b)
		if v == "" {
			continue
		}
		if _, ok := out[v]; ok {
			duplicate[v] = true
			continue
		}
		out[v] = i
	}
	for v := range duplicate {
		delete(out, v)
	}
	return out
}

func indexByBlock(blocks []*parser.Block) map[*parser.Block]int {
	out := make(map[*parser.Block]int, len(blocks))
	for i, b := range blocks {
		out[b] = i
	}
	return out
}

func previousSibling(doc *parser.Document, block *parser.Block) *parser.Block {
	if doc == nil || block == nil || block.ParentID == "" {
		return nil
	}
	parent := doc.ByID[block.ParentID]
	if parent == nil {
		return nil
	}
	var prev *parser.Block
	for _, childID := range parent.ChildIDs {
		child := doc.ByID[childID]
		if child == block {
			return prev
		}
		prev = child
	}
	return nil
}

func sectionPath(doc *parser.Document, block *parser.Block) []string {
	if doc == nil || block == nil {
		return nil
	}
	var reversed []string
	for parentID := block.ParentID; parentID != ""; {
		parent := doc.ByID[parentID]
		if parent == nil || parent == doc.Root {
			break
		}
		if parent.Kind == parser.KindSection {
			reversed = append(reversed, sectionName(parent))
		}
		parentID = parent.ParentID
	}
	path := make([]string, len(reversed))
	for i := range reversed {
		path[i] = reversed[len(reversed)-1-i]
	}
	return path
}

func sectionName(block *parser.Block) string {
	switch {
	case block.Label != "":
		return block.Label
	case block.Title != "":
		return block.Title
	case block.ID != "":
		return block.ID
	default:
		return fmt.Sprintf("section:%d", block.StartLine)
	}
}

func sectionPathKey(path []string) string {
	return strings.Join(path, "\x00")
}

func compatibleKind(oldBlock, newBlock *parser.Block) bool {
	if oldBlock == nil || newBlock == nil {
		return false
	}
	if oldBlock.Kind == newBlock.Kind {
		return true
	}
	return false
}

func nearSection(oldPath, newPath []string) bool {
	if sameStringSlice(oldPath, newPath) {
		return true
	}
	if len(oldPath) == 0 || len(newPath) == 0 {
		return true
	}
	return commonPrefixLen(oldPath, newPath) > 0
}

func fuzzyScore(oldBlock, newBlock *parser.Block, oldMeta, newMeta blockMeta) float64 {
	if len(oldMeta.Tokens) == 0 || len(newMeta.Tokens) == 0 {
		return 0
	}
	score := diceCoefficient(oldMeta.Tokens, newMeta.Tokens)
	if sameStringSlice(oldMeta.SectionPath, newMeta.SectionPath) {
		score += 0.06
	} else if prefix := commonPrefixLen(oldMeta.SectionPath, newMeta.SectionPath); prefix > 0 {
		score += math.Min(0.04, float64(prefix)*0.02)
	}
	distance := math.Abs(float64(oldMeta.Index - newMeta.Index))
	score -= math.Min(0.08, distance/200)
	if oldBlock.EnvName != "" && oldBlock.EnvName == newBlock.EnvName {
		score += 0.03
	}
	if score > 1 {
		return 1
	}
	if score < 0 {
		return 0
	}
	return score
}

func fuzzyThreshold(block *parser.Block) float64 {
	switch block.Kind {
	case parser.KindSection:
		return 0.82
	case parser.KindTheoremLike, parser.KindProof, parser.KindFigure:
		return 0.62
	case parser.KindDisplay:
		return 0.86
	case parser.KindParagraph, parser.KindProofStep:
		return 0.74
	default:
		return 0.72
	}
}

func fuzzyEligible(block *parser.Block, meta blockMeta) bool {
	switch block.Kind {
	case parser.KindParagraph, parser.KindProofStep:
		return len(meta.Tokens) >= 6 && len([]rune(meta.Normalized)) >= 32
	default:
		return true
	}
}

func duplicatedGenericProse(block *parser.Block, blocks []*parser.Block, meta []blockMeta, index int) bool {
	if block.Kind != parser.KindParagraph && block.Kind != parser.KindProofStep {
		return false
	}
	normalized := meta[index].Normalized
	if normalized == "" {
		return false
	}
	count := 0
	for i, other := range blocks {
		if other.Kind == block.Kind && meta[i].Normalized == normalized {
			count++
		}
	}
	return count > 1
}

func diceCoefficient(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	counts := map[string]int{}
	for _, tok := range a {
		counts[tok]++
	}
	overlap := 0
	for _, tok := range b {
		if counts[tok] > 0 {
			overlap++
			counts[tok]--
		}
	}
	return float64(2*overlap) / float64(len(a)+len(b))
}

func sameStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func commonPrefixLen(a, b []string) int {
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

var labelPattern = regexp.MustCompile(`\\label\s*\{[^{}]*\}`)

// NormalizeSourceForMatch normalizes LaTeX source for matching only.
func NormalizeSourceForMatch(src string) string {
	withoutComments := stripLatexComments(src)
	withoutLabels := labelPattern.ReplaceAllString(withoutComments, "")
	return strings.Join(strings.Fields(withoutLabels), " ")
}

func stripLatexComments(src string) string {
	var out strings.Builder
	for i := 0; i < len(src); i++ {
		if src[i] == '%' && !escapedAt(src, i) {
			for i < len(src) && src[i] != '\n' {
				i++
			}
			if i < len(src) && src[i] == '\n' {
				out.WriteByte('\n')
			}
			continue
		}
		out.WriteByte(src[i])
	}
	return out.String()
}

func escapedAt(src string, index int) bool {
	backslashes := 0
	for i := index - 1; i >= 0 && src[i] == '\\'; i-- {
		backslashes++
	}
	return backslashes%2 == 1
}

func matchTokens(normalized string) []string {
	var tokens []string
	for i := 0; i < len(normalized); {
		r, size := utf8.DecodeRuneInString(normalized[i:])
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			start := i
			i += size
			for i < len(normalized) {
				next, nextSize := utf8.DecodeRuneInString(normalized[i:])
				if !unicode.IsLetter(next) && !unicode.IsDigit(next) {
					break
				}
				i += nextSize
			}
			tokens = append(tokens, strings.ToLower(normalized[start:i]))
		case r == '\\':
			start := i
			i += size
			for i < len(normalized) {
				next, nextSize := utf8.DecodeRuneInString(normalized[i:])
				if !unicode.IsLetter(next) {
					break
				}
				i += nextSize
			}
			tokens = append(tokens, normalized[start:i])
		case unicode.IsSpace(r):
			i += size
		default:
			tokens = append(tokens, string(r))
			i += size
		}
	}
	return tokens
}

func hashString(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func applyEndpointFile(doc *parser.Document, endpoint Endpoint) {
	if doc == nil {
		return
	}
	file := endpoint.Path
	if file == "" {
		file = endpoint.RelPath
	}
	if file == "" {
		file = endpoint.Spec
	}
	doc.File = file
	for _, b := range doc.Blocks {
		if b.File == "" {
			b.File = file
		}
	}
}
