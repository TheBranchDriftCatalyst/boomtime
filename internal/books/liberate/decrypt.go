// decrypt.go — step 4 of liberation: strip the AAXC DRM and remux to M4B.
// See docs/design/catalyst-books-liberation-architecture.md §2.4.
//
// This file defines only the SEAM. The implementation is ffmpeg (ffmpeg.go), and
// the seam exists so it can be replaced without touching any caller: Libation
// wrote its own AAXClean rather than depending on ffmpeg, largely because
// ffmpeg's aax demuxer struggles with newer xHE-AAC titles. If that bites here,
// the native remuxer (design doc §10 epic D) drops in behind this interface.
//
// That swap is DATA-TRIGGERED, not a matter of taste: the pipeline records
// content_format on every row, so "how many of my titles can ffmpeg not handle"
// is a SQL question. The first live sample was AAX_44_128 (plain AAC-LC), which
// ffmpeg handles fine.
package liberate

import "context"

// DecryptRequest is everything one remux needs.
type DecryptRequest struct {
	// SrcPath is the downloaded, still-encrypted AAXC.
	SrcPath string
	// DstPath is where the DRM-free M4B is written. It is in the WORK dir, not
	// the library — the sink commits it afterwards (sink.go).
	DstPath string
	// Key is the per-book content key. A secret; never logged (see voucher.go).
	Key ContentKey
	// FFMetadataPath is an optional chapters file (BuildFFMetadata). Empty means
	// no chapter marks — the source metadata is stripped rather than passed
	// through, so a book without chapters is untagged rather than mis-tagged.
	FFMetadataPath string
	// CoverPath is an optional cover image to embed as attached_pic.
	CoverPath string
	// Meta supplies the -metadata tags.
	Meta Metadata
}

// Decryptor strips DRM and produces a tagged M4B.
type Decryptor interface {
	// Decrypt performs the remux. It must not log or embed the content key in
	// any error it returns.
	Decrypt(ctx context.Context, req DecryptRequest) error
	// Available reports whether the backend can actually run, so the server can
	// say so at startup instead of failing on the first book.
	Available(ctx context.Context) error
}
