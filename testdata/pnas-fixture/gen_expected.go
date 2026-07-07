//go:build ignore

// gen_expected.go regenerates the golden expected files for the PNAS fixture.
// Run with: go run testdata/pnas-fixture/gen_expected.go
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/lenis2000/mrevdiff/pkg/format"
)

func main() {
	dir := filepath.Dir(os.Args[0])
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	inputPath := filepath.Join(dir, "main_pnas.tex")
	expectedPath := filepath.Join(dir, "main_pnas.expected.tex")

	src, err := os.ReadFile(inputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading %s: %v\n", inputPath, err)
		os.Exit(1)
	}

	// Run the pipeline with Tier-1 + Tier-2 rules (no diagnostics).
	result := format.Apply(src, format.Options{
		PDFFix: true,
		Diag:   false,
	})

	if err := os.WriteFile(expectedPath, result.Src, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "error writing %s: %v\n", expectedPath, err)
		os.Exit(1)
	}

	fmt.Printf("generated %s (%d bytes, %d hits)\n", expectedPath, len(result.Src), len(result.Hits))
	for _, h := range result.Hits {
		fmt.Printf("  %s at L%d: %s\n", h.RuleID, h.Line, h.Excerpt)
	}
}
