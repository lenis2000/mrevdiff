package parser

import (
	"crypto/sha1"
	"encoding/hex"
	"strconv"
	"strings"
	"unicode"
)

// assignStableIDs rewrites the transient sequential IDs set during tree
// construction with stable, content-derived IDs that survive line drift
// between runs.
//
// Rules:
//   - If the block has a LaTeX label, ID = label.
//   - KindProof immediately following a labeled theorem-like sibling →
//     "<theoremLabel>.proof".
//   - KindProofStep under a proof → "<proofID>.step.<n>" (1-based).
//   - Otherwise ID = "<slug>-<siblingIdx>-<sha8>" where sha8 =
//     sha1(kind || parent-label || source[:40])[:8].
//
// After rewriting, ByID is rebuilt and ParentID/ChildIDs fields are patched.
func (p *parser) assignStableIDs() {
	idMap := map[string]string{"root": "root"}
	seen := map[string]bool{"root": true}
	assignIDs(p.doc, p.doc.Root, 0, "root", idMap, seen)

	for _, b := range p.doc.Blocks {
		if nid, ok := idMap[b.ID]; ok {
			b.ID = nid
		}
		if b.ParentID != "" {
			if nid, ok := idMap[b.ParentID]; ok {
				b.ParentID = nid
			}
		}
		for i, cid := range b.ChildIDs {
			if nid, ok := idMap[cid]; ok {
				b.ChildIDs[i] = nid
			}
		}
	}

	newByID := make(map[string]*Block, len(p.doc.Blocks))
	for _, b := range p.doc.Blocks {
		newByID[b.ID] = b
	}
	p.doc.ByID = newByID
}

func assignIDs(doc *Document, b *Block, siblingIdx int, parentNewID string, idMap map[string]string, seen map[string]bool) {
	if b != doc.Root {
		newID := deriveID(doc, b, siblingIdx, parentNewID, idMap)
		newID = ensureUnique(newID, seen)
		idMap[b.ID] = newID
		seen[newID] = true
		parentNewID = newID
	}
	// Track proof-step numbering so order-dependent ".step.N" is stable.
	stepN := 0
	for i, cid := range b.ChildIDs {
		c := doc.ByID[cid]
		if c.Kind == KindProofStep {
			stepN++
			// stash the step ordinal into siblingIdx for deriveID
			assignIDs(doc, c, stepN, parentNewID, idMap, seen)
			continue
		}
		assignIDs(doc, c, i, parentNewID, idMap, seen)
	}
}

func deriveID(doc *Document, b *Block, siblingIdx int, parentNewID string, idMap map[string]string) string {
	if b.Label != "" {
		return b.Label
	}
	if b.Kind == KindProof {
		if prev := previousSibling(doc, b); prev != nil && prev.Kind == KindTheoremLike && prev.Label != "" {
			return prev.Label + ".proof"
		}
	}
	if b.Kind == KindProofStep {
		return parentNewID + ".step." + strconv.Itoa(siblingIdx)
	}
	slug := computeSlug(b)
	parentLabel := ""
	if parent := doc.ByID[b.ParentID]; parent != nil {
		parentLabel = parent.Label
	}
	h := sha8(b.Kind.String() + "|" + parentLabel + "|" + truncate(b.Source, 40))
	return slug + "-" + strconv.Itoa(siblingIdx) + "-" + h
}

func previousSibling(doc *Document, b *Block) *Block {
	parent := doc.ByID[b.ParentID]
	if parent == nil {
		return nil
	}
	var prev *Block
	for _, cid := range parent.ChildIDs {
		if cid == b.ID {
			return prev
		}
		prev = doc.ByID[cid]
	}
	return nil
}

func computeSlug(b *Block) string {
	base := strings.ToLower(b.Kind.String())
	if b.Kind == KindSection && b.Title != "" {
		if s := slugify(b.Title); s != "" {
			return s
		}
	}
	if b.EnvName != "" {
		base = strings.ToLower(b.EnvName)
	}
	return base
}

func slugify(s string) string {
	var out []rune
	lastDash := false
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			out = append(out, unicode.ToLower(r))
			lastDash = false
		case r == ' ' || r == '-' || r == '_':
			if !lastDash && len(out) > 0 {
				out = append(out, '-')
				lastDash = true
			}
		}
	}
	for len(out) > 0 && out[len(out)-1] == '-' {
		out = out[:len(out)-1]
	}
	if len(out) > 32 {
		out = out[:32]
	}
	return string(out)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func sha8(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:])[:8]
}

func ensureUnique(id string, seen map[string]bool) string {
	if !seen[id] {
		return id
	}
	for i := 2; ; i++ {
		cand := id + "~" + strconv.Itoa(i)
		if !seen[cand] {
			return cand
		}
	}
}
