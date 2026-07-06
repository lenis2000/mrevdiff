package pdf

import "image/png"

// fastPNG trades compression ratio for encode speed (~3-5x faster than the
// default level). Every PNG this package produces is a short-lived frame
// handed straight to the terminal (or a small cache), so encode latency is
// on the interactive path while file size is nearly irrelevant.
var fastPNG = png.Encoder{CompressionLevel: png.BestSpeed}
