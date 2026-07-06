package parser

import "fmt"

// Kind categorizes a Block semantically. The tree model distinguishes a small
// number of LaTeX constructs so that the UI can navigate and render them
// differently.
type Kind int

const (
	KindSection Kind = iota
	KindAbstract
	KindTheoremLike
	KindProof
	KindDisplay
	KindFigure
	KindParagraph
	KindProofStep
	KindBibliography
	KindOther
)

// String returns a short human-readable name for a Kind.
func (k Kind) String() string {
	switch k {
	case KindSection:
		return "Section"
	case KindAbstract:
		return "Abstract"
	case KindTheoremLike:
		return "TheoremLike"
	case KindProof:
		return "Proof"
	case KindDisplay:
		return "Display"
	case KindFigure:
		return "Figure"
	case KindParagraph:
		return "Paragraph"
	case KindProofStep:
		return "ProofStep"
	case KindBibliography:
		return "Bibliography"
	case KindOther:
		return "Other"
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// Region is a rectangular area on a PDF page. Populated by pkg/synctex in a
// later task; left nil until then.
type Region struct {
	Page       int
	X, Y, W, H float64
}

// Ref records an outgoing reference from a block's source.
// LineOffset / ColOffset are measured from the block's start: LineOffset 0
// means the ref is on the same line as StartLine; ColOffset is 0-based.
type Ref struct {
	Kind       string // "ref", "cref", "Cref", "eqref", "cite"
	Target     string
	LineOffset int
	ColOffset  int
	Resolved   bool
}

// Block is a node in the semantic tree produced by Parse.
//
// The ID and Number fields, plus fully populated RefsOut and PDFRegion, are
// filled in by downstream tasks; during Task 3 the parser assigns temporary
// sequential IDs that later passes will overwrite.
type Block struct {
	ID                 string
	EnvName            string
	File               string
	Kind               Kind
	StartLine, EndLine int
	Source             string
	Title              string
	Label              string
	Number             string
	RefsOut            []Ref
	ParentID           string
	ChildIDs           []string
	PDFRegion          *Region
}
