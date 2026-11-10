package veo

import (
	"strings"

	"github.com/shouni/go-veo-orchestrator/ports"
	"github.com/shouni/go-veo-orchestrator/video"
)

// GenerationMode is which Veo generation feature a single video generation request
// is interpreted as.
//
// The mode is decided once per request by ClassifyRequest, and the Veo request body
// the adapter builds, the durations that request allows (DurationsForMode) and the
// choice among prompts whose assumptions differ per generation mode all share this
// one value. When the decisions split, you get meaningless requests such as telling
// the model to "match the reference image" while not sending the reference image.
type GenerationMode string

const (
	// ModeImageToVideo is image_to_video, with the keyframe image as image input.
	// Requests with no image reference at all (text only) fall here too.
	ModeImageToVideo GenerationMode = "image_to_video"
	// ModeFramesToVideo is first/last frame interpolation, with the start frame as
	// image input and the end frame as lastFrame input (Veo 2 / Veo 3.1 models
	// only, Fast included).
	ModeFramesToVideo GenerationMode = "frames_to_video"
	// ModeReferenceToVideo is reference_to_video, with [character art, keyframe]
	// as referenceImages (non-Fast Veo 3 models only, fixed at 8 seconds).
	ModeReferenceToVideo GenerationMode = "reference_to_video"
	// ModeVideoExtension is video_extension (video-to-video continuation), with the
	// previous cut's video as video input. No image reference is sent in this mode.
	ModeVideoExtension GenerationMode = "video_extension"
)

// Capabilities describes whether the ports.VideoRunner / model actually in use
// supports Veo's optional features. It is an input to ClassifyRequest: an adapter
// derives it from the model name, a caller from RunnerCapabilities (the Runner's
// optional interfaces).
type Capabilities struct {
	// ReferenceImages is support for referenceImages (reference_to_video, fixed at 8 seconds).
	ReferenceImages bool
	// LastFrame is support for lastFrame (first/last frame interpolation).
	LastFrame bool
}

// RunnerCapabilities derives Capabilities from the optional interfaces of a
// ports.VideoRunner (ReferenceImagesSupporter / LastFrameSupporter). A Runner
// implementing neither (a test double, say) reports false for each feature and
// falls back to image_to_video.
func RunnerCapabilities(runner ports.VideoRunner) Capabilities {
	caps := Capabilities{}
	if rs, ok := runner.(ports.ReferenceImagesSupporter); ok {
		caps.ReferenceImages = rs.SupportsReferenceImages()
	}
	if ls, ok := runner.(ports.LastFrameSupporter); ok {
		caps.LastFrame = ls.SupportsLastFrame()
	}
	return caps
}

// ClassifyRequest decides which Veo generation feature this request is interpreted
// as. It is the only branch point that decides the mode, with this precedence:
//
//  1. video_extension — when usePreviousVideo is enabled and PreviousVideoURI is a
//     gs:// reference. Veo cannot combine video with referenceImages / image, so
//     every image reference after this is ignored.
//  2. reference_to_video — when there is at least one reference image URI and the
//     model supports referenceImages.
//  3. frames_to_video — when both a start frame (ImageReference or InputImage) and
//     LastFrameReference are present and the model supports lastFrame (Veo's
//     lastFrame is valid only together with image).
//  4. image_to_video — everything else.
func ClassifyRequest(req video.GenerationRequest, usePreviousVideo bool, caps Capabilities) GenerationMode {
	if usePreviousVideo && strings.HasPrefix(strings.TrimSpace(req.PreviousVideoURI), "gs://") {
		return ModeVideoExtension
	}
	if caps.ReferenceImages && hasAnyReferenceImage(req.ReferenceImages) {
		return ModeReferenceToVideo
	}
	if caps.LastFrame && hasStartImage(req) && strings.TrimSpace(req.LastFrameReference) != "" {
		return ModeFramesToVideo
	}
	return ModeImageToVideo
}

// hasAnyReferenceImage reports whether there is at least one reference image,
// ignoring blank entries (the counterpart of the adapter's rule that drops blank
// URIs and treats them as no reference).
func hasAnyReferenceImage(refs []string) bool {
	for _, ref := range refs {
		if strings.TrimSpace(ref) != "" {
			return true
		}
	}
	return false
}

// hasStartImage reports whether there is an image input to serve as the start
// frame (a GCS reference or inline bytes).
func hasStartImage(req video.GenerationRequest) bool {
	return strings.TrimSpace(req.ImageReference) != "" || len(req.InputImage) > 0
}

// ModelCapabilities derives support for the optional features from a Veo model
// name. This function is the only definition of the model-name → features rule
// (the adapter used to re-derive it by string prefix matching, breaking the
// principle that "the library owns the rules").
//
//   - referenceImages (reference_to_video, fixed at 8 seconds): Veo 3 models only.
//     Fast is not supported.
//   - lastFrame (first/last frame interpolation): veo-2.0 / veo-3.1 models (Fast
//     included). Veo 3.0 models are not supported.
func ModelCapabilities(model string) Capabilities {
	m := strings.ToLower(strings.TrimSpace(model))
	return Capabilities{
		ReferenceImages: strings.HasPrefix(m, "veo-3") && !strings.Contains(m, "fast"),
		LastFrame:       strings.HasPrefix(m, "veo-2") || strings.HasPrefix(m, "veo-3.1"),
	}
}
