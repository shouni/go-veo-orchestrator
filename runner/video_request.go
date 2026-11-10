package runner

import (
	"fmt"
	"slices"
	"strings"

	characterkit "github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-veo-orchestrator/veo"
	"github.com/shouni/go-veo-orchestrator/video"
)

// BuildInput is the full set of inputs for assembling one cut's video generation
// request. The earlier positional arguments (recipe, cut, keyframe,
// previousVideoID) had nowhere to pass lastFrame or the model's capabilities, and
// assumed the caller would rewrite Build()'s return value afterwards, so the
// inputs now come in a struct.
type BuildInput struct {
	// Recipe is the recipe the cut belongs to (used for the prompt's Music mood and for the Seed).
	Recipe *video.Recipe
	// Cut is the cut to generate.
	Cut video.Cut
	// PreviousVideoURI is the gs:// URI of the previous cut's video, carried over
	// as the context of a video-to-video continuation. An empty string means a
	// chain start (no continuation).
	PreviousVideoURI string
	// LastFrameReference is the end frame for frames_to_video interpolation
	// (normally the return value of Cuts.NextLastFrameReference).
	LastFrameReference string
	// Capabilities are the optional Veo features supported by the VideoRunner /
	// model actually in use (derived with veo.RunnerCapabilities). The zero value
	// is treated as "no optional features" and falls back to image_to_video.
	Capabilities veo.Capabilities
}

// VideoRequestBuilder assembles video generation requests from cut information.
type VideoRequestBuilder interface {
	Build(in BuildInput) video.GenerationRequest
}

// DefaultVideoRequestBuilder is the standard video generation request builder.
// When characters is set, it assembles the cut's character art and keyframe as
// ReferenceImages (Veo's referenceImages, asset type, up to 3).
type DefaultVideoRequestBuilder struct {
	characters *characterkit.Characters
}

// NewVideoRequestBuilder initializes a DefaultVideoRequestBuilder.
func NewVideoRequestBuilder() *DefaultVideoRequestBuilder {
	return &DefaultVideoRequestBuilder{}
}

// NewVideoRequestBuilderWithCharacters initializes a builder that assembles the
// character art as referenceImages. When characters is nil, it behaves the same as
// the standard builder.
func NewVideoRequestBuilderWithCharacters(characters *characterkit.Characters) *DefaultVideoRequestBuilder {
	return &DefaultVideoRequestBuilder{characters: characters}
}

// Build constructs the Veo request from the recipe, the cut and the keyframe
// result.
//
// The assembled request is classified with veo.ClassifyRequest, and inputs Veo
// would not actually use in that mode are dropped before it is returned (e.g. no
// image references under video_extension, no LastFrameReference for models without
// lastFrame support). Making "the assembled request" match "the request the
// adapter sends to Veo" spares logs and reproductions from chasing inputs that
// were never actually sent.
func (b *DefaultVideoRequestBuilder) Build(in BuildInput) video.GenerationRequest {
	cut := in.Cut

	// The keyframe is saved, and its reference (KeyframeReference) and seed
	// (KeyframeSeed) are recorded on the cut. Use that seed to carry the same
	// random sequence over to the video, falling back to the character's / recipe's
	// seed only when none is recorded.
	seed := cut.KeyframeSeed
	if seed == 0 {
		seed = fallbackSeed(in.Recipe, cut, b.characters)
	}

	// Only a gs:// video reference means anything in PreviousVideoURI. If an
	// operation name or the like slips in, classification falls to image_to_video
	// while the field remains, leaving room for the adapter to build a
	// video_extension by its own judgment, so it is removed here.
	previousVideoURI := strings.TrimSpace(in.PreviousVideoURI)
	if !strings.HasPrefix(previousVideoURI, "gs://") {
		previousVideoURI = ""
	}

	req := video.GenerationRequest{
		ImageReference:     cut.KeyframeReference,
		ReferenceImages:    video.CutReferenceImages(cut, b.characters),
		AudioReference:     cut.AudioReference,
		PreviousVideoURI:   previousVideoURI,
		LastFrameReference: in.LastFrameReference,
		Seed:               seed,
		CutIndex:           cut.CutIndex,
		DurationSec:        cut.DurationSec,
	}

	// An empty PreviousVideoURI is the caller saying "this cut is a chain start,
	// so do not continue". Only when it is non-empty is the cut a candidate for
	// video_extension.
	usePreviousVideo := previousVideoURI != ""
	pruneUnusedInputs(&req, veo.ClassifyRequest(req, usePreviousVideo, in.Capabilities))
	req.Prompt = b.buildPrompt(in.Recipe, cut)

	return req
}

// pruneUnusedInputs drops from the request the inputs Veo does not reference in the
// resolved generation mode. Veo cannot combine video with referenceImages / image,
// and lastFrame is valid only together with image, so leaving them in would just
// get them ignored, and in the logs they would look like "inputs that were sent but
// had no effect".
func pruneUnusedInputs(req *video.GenerationRequest, mode veo.GenerationMode) {
	switch mode {
	case veo.ModeVideoExtension:
		req.ReferenceImages = nil
		req.ImageReference = ""
		req.InputImage = nil
		req.LastFrameReference = ""
	case veo.ModeReferenceToVideo:
		req.LastFrameReference = ""
		req.PreviousVideoURI = ""
	case veo.ModeFramesToVideo:
		req.ReferenceImages = nil
		req.PreviousVideoURI = ""
	case veo.ModeImageToVideo:
		req.ReferenceImages = nil
		req.LastFrameReference = ""
		req.PreviousVideoURI = ""
	}
}

// fallbackSeed returns the seed to use when none could be obtained from the
// keyframe result. The seed tied to the cut's character (the same value keyframe
// image generation uses) takes priority, then the recipe-wide seed. Independent
// generation without a seed (0 = omitted from the Veo request) wobbles in look at
// each chain start by chance, so passing a seed fixed at least per character
// strengthens character consistency.
func fallbackSeed(recipe *video.Recipe, cut video.Cut, characters *characterkit.Characters) int64 {
	if characters != nil {
		if char := characters.GetCharacter(cut.CharacterID); char != nil && char.Seed != nil {
			return *char.Seed
		}
	}
	if recipe != nil && recipe.MusicRecipe.Seed != nil {
		return *recipe.MusicRecipe.Seed
	}
	return 0
}

func (b *DefaultVideoRequestBuilder) buildPrompt(recipe *video.Recipe, cut video.Cut) string {
	parts := []string{
		strings.TrimSpace(cut.VisualAnchor),
	}
	if cut.AudioCue != "" {
		parts = append(parts, fmt.Sprintf("Synchronize motion and camera timing with audio cue: %s", cut.AudioCue))
	}
	if recipe != nil {
		if musicMood := strings.TrimSpace(recipe.MusicRecipe.Mood); musicMood != "" {
			parts = append(parts, "Music mood: "+musicMood)
		}
	}
	if cut.StartSec != 0 || cut.EndSec != 0 {
		parts = append(parts, fmt.Sprintf("Timeline: %.2fs to %.2fs.", cut.StartSec, cut.EndSec))
	}

	// VisualAnchor can be empty, so drop empty elements before joining.
	parts = slices.DeleteFunc(parts, func(part string) bool {
		return strings.TrimSpace(part) == ""
	})
	return strings.Join(parts, "\n")
}
