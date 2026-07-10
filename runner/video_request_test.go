package runner

import (
	"testing"

	characterkit "github.com/shouni/go-character-kit/character"

	"github.com/shouni/go-veo-orchestrator/ports"
)

func newTestCharacters() *characterkit.Characters {
	chars := &characterkit.Characters{
		List: []characterkit.Character{
			{ID: "zundamon", Name: "ずんだもん", ReferenceURL: "gs://bucket/characters/zundamon.png"},
			{ID: "no-ref", Name: "参照なし"},
		},
	}
	chars.ByID = map[string]*characterkit.Character{
		"zundamon": &chars.List[0],
		"no-ref":   &chars.List[1],
	}
	return chars
}

// TestVideoRequestBuilderWithCharactersBuildsReferenceImages は、立ち絵とキーフレームが
// referenceImages として組み立てられることを検証します。
func TestVideoRequestBuilderWithCharactersBuildsReferenceImages(t *testing.T) {
	builder := NewVideoRequestBuilderWithCharacters(newTestCharacters())
	recipe := &ports.VideoRecipe{ProjectTitle: "test"}
	cut := ports.Cut{
		CutIndex:          1,
		DurationSec:       8,
		VisualAnchor:      "anchor",
		CharacterID:       "zundamon",
		KeyframeReference: "gs://bucket/jobs/job-1/images/cut_1.png",
	}

	req := builder.Build(recipe, cut, nil, "")

	want := []string{
		"gs://bucket/characters/zundamon.png",
		"gs://bucket/jobs/job-1/images/cut_1.png",
	}
	if len(req.ReferenceImages) != len(want) {
		t.Fatalf("ReferenceImages = %v, want %v", req.ReferenceImages, want)
	}
	for i := range want {
		if req.ReferenceImages[i] != want[i] {
			t.Fatalf("ReferenceImages[%d] = %q, want %q", i, req.ReferenceImages[i], want[i])
		}
	}
	// image-to-video フォールバック用に ImageReference も維持される。
	if req.ImageReference != cut.KeyframeReference {
		t.Fatalf("ImageReference = %q, want keyframe reference", req.ImageReference)
	}
}

// TestVideoRequestBuilderReferenceImagesFallsBackWithoutCharacter は、キャラクター未解決や
// 立ち絵未設定の場合に referenceImages を組み立てず image-to-video に委ねることを検証します。
func TestVideoRequestBuilderReferenceImagesFallsBackWithoutCharacter(t *testing.T) {
	tests := []struct {
		name    string
		builder *DefaultVideoRequestBuilder
		cut     ports.Cut
	}{
		{
			name:    "characters not configured",
			builder: NewVideoRequestBuilder(),
			cut:     ports.Cut{CharacterID: "zundamon", KeyframeReference: "gs://bucket/kf.png"},
		},
		{
			name:    "unknown character",
			builder: NewVideoRequestBuilderWithCharacters(newTestCharacters()),
			cut:     ports.Cut{CharacterID: "unknown", KeyframeReference: "gs://bucket/kf.png"},
		},
		{
			name:    "character without reference url",
			builder: NewVideoRequestBuilderWithCharacters(newTestCharacters()),
			cut:     ports.Cut{CharacterID: "no-ref", KeyframeReference: "gs://bucket/kf.png"},
		},
	}
	recipe := &ports.VideoRecipe{ProjectTitle: "test"}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.cut.CutIndex = 1
			tt.cut.DurationSec = 8
			tt.cut.VisualAnchor = "anchor"
			req := tt.builder.Build(recipe, tt.cut, nil, "")
			if req.ReferenceImages != nil {
				t.Fatalf("ReferenceImages = %v, want nil", req.ReferenceImages)
			}
			if req.ImageReference != tt.cut.KeyframeReference {
				t.Fatalf("ImageReference = %q, want keyframe reference", req.ImageReference)
			}
		})
	}
}
