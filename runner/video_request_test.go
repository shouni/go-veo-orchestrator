package runner

import (
	"testing"

	"github.com/shouni/genai-kit/lyria"
	characterkit "github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-veo-orchestrator/veo"
	"github.com/shouni/go-veo-orchestrator/video"
)

func newTestCharacters(t *testing.T) *characterkit.Characters {
	t.Helper()
	chars, err := characterkit.NewCharacters([]characterkit.Character{
		{
			ID:           "zundamon",
			Name:         "ずんだもん",
			ReferenceURL: "gs://bucket/characters/zundamon.png",
			VisualCues:   []string{"green hair"},
		},
		{
			// A character without a Seed (for verifying the seed fallback).
			ID:           "metan",
			Name:         "めたん",
			ReferenceURL: "gs://bucket/characters/metan.png",
			VisualCues:   []string{"purple hair"},
		},
	})
	if err != nil {
		t.Fatalf("NewCharacters() error = %v", err)
	}
	return chars
}

// refCaps represents a model supporting referenceImages (non-Fast Veo 3).
var refCaps = veo.Capabilities{ReferenceImages: true}

func assertStrings(t *testing.T, label string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s[%d] = %q, want %q", label, i, got[i], want[i])
		}
	}
}

// TestVideoRequestBuilderWithCharactersBuildsReferenceImages verifies that when the
// model supports referenceImages, the character art and keyframe are assembled as
// referenceImages.
func TestVideoRequestBuilderWithCharactersBuildsReferenceImages(t *testing.T) {
	builder := NewVideoRequestBuilderWithCharacters(newTestCharacters(t))
	cut := video.Cut{
		CutIndex:          1,
		VisualAnchor:      "anchor",
		CharacterID:       "zundamon",
		DurationSec:       8,
		KeyframeReference: "gs://bucket/jobs/job-1/images/cut_1.png",
	}

	req := builder.Build(BuildInput{
		Recipe:       &video.Recipe{ProjectTitle: "test"},
		Cut:          cut,
		Capabilities: refCaps,
	})

	assertStrings(t, "ReferenceImages", req.ReferenceImages, []string{
		"gs://bucket/characters/zundamon.png",
		"gs://bucket/jobs/job-1/images/cut_1.png",
	})
	// ImageReference is kept too, for the image-to-video fallback.
	if req.ImageReference != cut.KeyframeReference {
		t.Fatalf("ImageReference = %q, want keyframe reference", req.ImageReference)
	}
}

// TestVideoRequestBuilderReferenceImagesNeedsModelSupport verifies that when the
// model does not support referenceImages (the zero value of VeoCapabilities),
// referenceImages is not sent and the request falls back to image_to_video's
// keyframe input. The adapter falls back on the same decision, so the request is
// aligned with what is actually sent from the assembly stage on.
func TestVideoRequestBuilderReferenceImagesNeedsModelSupport(t *testing.T) {
	builder := NewVideoRequestBuilderWithCharacters(newTestCharacters(t))
	cut := video.Cut{
		CutIndex:          1,
		VisualAnchor:      "anchor",
		CharacterID:       "zundamon",
		DurationSec:       8,
		KeyframeReference: "gs://bucket/kf.png",
	}

	req := builder.Build(BuildInput{Recipe: &video.Recipe{ProjectTitle: "test"}, Cut: cut})

	if req.ReferenceImages != nil {
		t.Fatalf("ReferenceImages = %v, want nil without model support", req.ReferenceImages)
	}
	if req.ImageReference != cut.KeyframeReference {
		t.Fatalf("ImageReference = %q, want keyframe reference", req.ImageReference)
	}
}

// TestVideoRequestBuilderOmitsImageInputsWithPreviousVideo verifies that when there
// is a video-to-video context (a gs:// PreviousVideoURI), no image input is sent
// at all. Veo does not accept video together with referenceImages / image.
func TestVideoRequestBuilderOmitsImageInputsWithPreviousVideo(t *testing.T) {
	builder := NewVideoRequestBuilderWithCharacters(newTestCharacters(t))
	recipe := &video.Recipe{ProjectTitle: "test"}
	cut := video.Cut{
		CutIndex:          2,
		VisualAnchor:      "anchor",
		CharacterID:       "zundamon",
		DurationSec:       7,
		KeyframeReference: "gs://bucket/jobs/job-1/images/cut_2.png",
	}

	// (a) With a gs:// PreviousVideoURI → video_extension, so image inputs are dropped.
	withPrev := builder.Build(BuildInput{
		Recipe:           recipe,
		Cut:              cut,
		PreviousVideoURI: "gs://bucket/videos/cut_1.mp4",
		Capabilities:     refCaps,
	})
	if withPrev.PreviousVideoURI != "gs://bucket/videos/cut_1.mp4" {
		t.Fatalf("PreviousVideoURI = %q", withPrev.PreviousVideoURI)
	}
	if len(withPrev.ReferenceImages) != 0 || withPrev.ImageReference != "" {
		t.Fatalf("image inputs = %v / %q, want none for video_extension", withPrev.ReferenceImages, withPrev.ImageReference)
	}

	// (b) Without PreviousVideoURI → referenceImages are assembled for the same cut/recipe.
	withoutPrev := builder.Build(BuildInput{Recipe: recipe, Cut: cut, Capabilities: refCaps})
	assertStrings(t, "ReferenceImages", withoutPrev.ReferenceImages, []string{
		"gs://bucket/characters/zundamon.png",
		"gs://bucket/jobs/job-1/images/cut_2.png",
	})
}

// TestVideoRequestBuilderReferenceImagesWithoutCharacterArt verifies that even when
// the character cannot be resolved (unset or unknown ID), the cut's own keyframe
// is used as referenceImages. video.CutReferenceImages is the only rule for
// building referenceImages, and the request's generation-mode decision looks at
// this same list.
func TestVideoRequestBuilderReferenceImagesWithoutCharacterArt(t *testing.T) {
	tests := []struct {
		name    string
		builder *DefaultVideoRequestBuilder
		cut     video.Cut
	}{
		{
			name:    "characters not configured",
			builder: NewVideoRequestBuilder(),
			cut:     video.Cut{CharacterID: "zundamon"},
		},
		{
			name:    "unknown character",
			builder: NewVideoRequestBuilderWithCharacters(newTestCharacters(t)),
			cut:     video.Cut{CharacterID: "unknown"},
		},
	}
	recipe := &video.Recipe{ProjectTitle: "test"}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.cut.CutIndex = 1
			tt.cut.DurationSec = 8
			tt.cut.VisualAnchor = "anchor"
			tt.cut.KeyframeReference = "gs://bucket/kf.png"

			req := tt.builder.Build(BuildInput{Recipe: recipe, Cut: tt.cut, Capabilities: refCaps})

			assertStrings(t, "ReferenceImages", req.ReferenceImages, []string{"gs://bucket/kf.png"})
			if req.ImageReference != tt.cut.KeyframeReference {
				t.Fatalf("ImageReference = %q, want keyframe reference", req.ImageReference)
			}
		})
	}
}

// TestVideoRequestBuilderLastFrameNeedsModelSupport verifies that lastFrame
// (frames_to_video interpolation) stays in the request only for supporting models.
// Sending it to an unsupporting model would just get it ignored, so it is dropped
// at assembly to match what is actually sent.
func TestVideoRequestBuilderLastFrameNeedsModelSupport(t *testing.T) {
	builder := NewVideoRequestBuilder()
	recipe := &video.Recipe{ProjectTitle: "test"}
	cut := video.Cut{
		CutIndex:          1,
		VisualAnchor:      "anchor",
		DurationSec:       8,
		KeyframeReference: "gs://bucket/cut_1.png",
	}

	supported := builder.Build(BuildInput{
		Recipe:             recipe,
		Cut:                cut,
		LastFrameReference: "gs://bucket/cut_2.png",
		Capabilities:       veo.Capabilities{LastFrame: true},
	})
	if supported.LastFrameReference != "gs://bucket/cut_2.png" {
		t.Fatalf("LastFrameReference = %q, want it kept for a supporting model", supported.LastFrameReference)
	}

	unsupported := builder.Build(BuildInput{
		Recipe:             recipe,
		Cut:                cut,
		LastFrameReference: "gs://bucket/cut_2.png",
	})
	if unsupported.LastFrameReference != "" {
		t.Fatalf("LastFrameReference = %q, want it dropped without model support", unsupported.LastFrameReference)
	}
}

// TestVideoRequestBuilderFallsBackToCharacterSeed verifies that when no seed can be
// obtained from the keyframe result, the request falls back to the character's seed
// (the same value keyframe generation uses), and failing that to the recipe-wide
// seed.
func TestVideoRequestBuilderFallsBackToCharacterSeed(t *testing.T) {
	charSeed := int64(4242)
	chars := newTestCharacters(t).WithSeedOverride("zundamon", charSeed)
	recipeSeed := int64(777)
	recipe := &video.Recipe{
		ProjectTitle: "test",
		MusicRecipe:  video.MusicRecipe{AIModels: lyria.AIModels{Seed: &recipeSeed}},
	}
	builder := NewVideoRequestBuilderWithCharacters(chars)

	withChar := builder.Build(BuildInput{
		Recipe: recipe,
		Cut:    video.Cut{CutIndex: 1, CharacterID: "zundamon", AudioSync: video.AudioSync{DurationSec: 8}},
	})
	if withChar.Seed != charSeed {
		t.Fatalf("Seed = %d, want character seed %d", withChar.Seed, charSeed)
	}

	withoutChar := builder.Build(BuildInput{
		Recipe: recipe,
		Cut:    video.Cut{CutIndex: 2, CharacterID: "metan", AudioSync: video.AudioSync{DurationSec: 8}},
	})
	if withoutChar.Seed != recipeSeed {
		t.Fatalf("Seed = %d, want recipe seed %d", withoutChar.Seed, recipeSeed)
	}
}
