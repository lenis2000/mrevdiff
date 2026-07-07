package format

// Registry is the ordered list of all formatting rules. Rules run in
// registration order; Tier-1 (Safe) rules run first, then Tier-2 (PDFFix),
// then Tier-3 (Diag).
var Registry []Rule

func init() {
	registerSafeRules()
	registerIndentRule()    // runs after the other Safe rules so leading-ws is the last word
	registerWrapRule()      // runs after indent so the lead prefix is stable
	registerMathAlignRule() // Tier 1: align & columns in math/tabular envs
	registerMathContRule()  // Tier 1: continuation-indent in equation envs
	registerMathWrapRule()  // Tier 1: wrap long equation rows at break ops (opt-in)
	registerPDFFixRules()
	registerTildeRule() // Tier 2: insert ~ before \cite/\ref commands
	registerDiagRules()
}
