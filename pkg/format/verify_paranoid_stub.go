//go:build !pdfverify

package format

import (
	"context"
	"fmt"
)

// ParanoidAvailable reports whether the paranoid verifier (diff-pdf pixel
// comparison) was compiled in. This build does NOT have the pdfverify tag.
const ParanoidAvailable = false

// VerifyParanoid is a stub that returns an error when the pdfverify build tag
// is not set. Build with -tags=pdfverify to enable pixel-level verification.
func VerifyParanoid(ctx context.Context, beforePDF, afterPDF string) (*ParanoidResult, error) {
	return nil, fmt.Errorf("paranoid verifier not available: build with -tags=pdfverify")
}
