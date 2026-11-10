package video

// KeyframeImage is one generated keyframe image and its metadata.
//
// Exposing imagegen.Response on the public API as-is would force the image kit on
// this library's consumers (Workflows consumers and test fakes). That leak was in
// fact the only reason ap-mv depended on the image kit directly, so the library
// keeps a type of its own.
type KeyframeImage struct {
	// Data is the image bytes.
	Data []byte
	// MimeType is the image's MIME type (used to decide the saved extension and Content-Type).
	MimeType string
	// UsedSeed is the seed used for generation. The image kit picks a seed
	// automatically by default, so this normally holds the value actually sent.
	UsedSeed int64
	// Model is the name of the model used for generation.
	Model string
	// Prompt is the final prompt actually sent.
	Prompt string
}
