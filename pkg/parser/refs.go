package parser

// resolveRefs attaches each TokRef in the source to the innermost block that
// contains its line and resolves the target against doc.ByLabel.
//
// Labels are already collected during buildTree (pkg/parser/parse.go), which
// is the first pass. This is the second pass.
func (p *parser) resolveRefs() {
	for _, tk := range p.tokens {
		if tk.Kind != TokRef {
			continue
		}
		host := p.innermostBlockAt(tk.Line)
		if host == nil || host == p.doc.Root {
			continue
		}
		_, resolved := p.doc.ByLabel[tk.Target]
		if tk.RefKind == "cite" {
			resolved = false // cite resolution is bbl-driven (Task 5); leave false for now
		}
		host.RefsOut = append(host.RefsOut, Ref{
			Kind:       tk.RefKind,
			Target:     tk.Target,
			LineOffset: tk.Line - host.StartLine,
			ColOffset:  tk.Col - 1,
			Resolved:   resolved,
		})
	}
}

// innermostBlockAt returns the deepest block in the tree whose [StartLine,
// EndLine] range contains the given line. Falls back to Root if nothing else
// matches.
func (p *parser) innermostBlockAt(line int) *Block {
	return descend(p.doc, p.doc.Root, line)
}

func descend(doc *Document, b *Block, line int) *Block {
	for _, cid := range b.ChildIDs {
		c := doc.ByID[cid]
		if c.StartLine <= line && line <= c.EndLine {
			return descend(doc, c, line)
		}
	}
	return b
}
