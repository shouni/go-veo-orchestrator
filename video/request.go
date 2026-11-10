package video

// GenerationRequest encapsulates the multimodal input passed to the Veo API.
type GenerationRequest struct {
	Prompt string
	// When ImageReference is set, the Veo adapter prefers it. Only when it is
	// empty is InputImage expected to be uploaded to produce a reference URI.
	ImageReference string
	// ReferenceImages are GCS URIs of several reference images, such as character
	// art and the keyframe. It maps to the Veo API's referenceImages field and
	// takes up to 3. When set, it takes priority over ImageReference.
	ReferenceImages []string
	AudioReference  string
	InputImage      []byte
	InputAudio      []byte
	// PreviousVideoURI is the gs:// URI of the video carried over as the previous
	// cut's Video-to-Video context. Veo's video input accepts only GCS references,
	// so a non-gs:// value (an operation name, etc.) is never classified as
	// video_extension (veo.ClassifyRequest / DefaultVideoRequestBuilder drop it).
	// The Veo API does not accept video and referenceImages together, so the
	// adapter uses it only when usePreviousVideo is enabled.
	PreviousVideoURI string
	// LastFrameReference is the GCS URI of the image used as the video's end frame
	// (Veo first/last frame interpolation). The Veo API requires it to be paired
	// with an image (the start frame), so the adapter sends it as lastFrame only
	// for image input (image_to_video). Supported only by Veo 2 / Veo 3.1 models.
	//
	// The value is passed to Build() as runner.BuildInput.LastFrameReference
	// (normally the return value of Cuts.NextLastFrameReference, which holds the
	// last-frame guards: section boundary, character change, identical keyframe).
	// Build() drops it for requests that do not resolve to frames_to_video, so a
	// request that still carries this field always actually uses lastFrame.
	LastFrameReference string
	Seed               int64
	CutIndex           int
	DurationSec        float64
}

// Response is the metadata of a generated video.
type Response struct {
	CloudURL string
	// VideoID identifies the generated video, and by contract is a gs:// URI that
	// can be passed as-is as the next cut's PreviousVideoURI. For a generation with
	// no GCS output (when no URI is available), leave it empty — filling it with an
	// operation name or the like keeps the next cut from being classified as
	// video_extension, and its duration, planned as a fixed 7 seconds, is then
	// rejected with ErrUnsupportedCutDuration.
	VideoID     string
	CutIndex    int
	DurationSec float64
	MimeType    string
	SizeBytes   int64
}
