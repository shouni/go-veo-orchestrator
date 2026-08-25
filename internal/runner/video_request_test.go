package runner

import (
	"testing"

	characterkit "github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-gemini-client/lyria"
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
			// Seed を持たないキャラクター（シードのフォールバック検証用）。
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

// refCaps は referenceImages に対応したモデル（Veo 3系の非Fast）を表します。
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

// TestVideoRequestBuilderWithCharactersBuildsReferenceImages は、モデルが referenceImages に
// 対応している場合に立ち絵とキーフレームが referenceImages として組み立てられることを
// 検証します。
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
	// image-to-video フォールバック用に ImageReference も維持される。
	if req.ImageReference != cut.KeyframeReference {
		t.Fatalf("ImageReference = %q, want keyframe reference", req.ImageReference)
	}
}

// TestVideoRequestBuilderReferenceImagesNeedsModelSupport は、モデルが referenceImages に
// 対応していない場合（VeoCapabilities のゼロ値）に referenceImages を送らず、
// image_to_video のキーフレーム入力へフォールバックすることを検証します。adapter が
// 同じ判定でフォールバックするため、組み立て段階から実際に送られる内容と揃えます。
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

// TestVideoRequestBuilderOmitsImageInputsWithPreviousVideo は、video-to-video の文脈
// （gs:// の PreviousVideoURI）がある場合に画像入力を一切送らないことを検証します。
// Veo は video と referenceImages / image を同時に受け付けません。
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

	// (a) gs:// の PreviousVideoURI あり → video_extension なので画像入力は落ちる。
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

	// (b) PreviousVideoURI なし → 同じ cut/recipe で referenceImages が組み立てられる。
	withoutPrev := builder.Build(BuildInput{Recipe: recipe, Cut: cut, Capabilities: refCaps})
	assertStrings(t, "ReferenceImages", withoutPrev.ReferenceImages, []string{
		"gs://bucket/characters/zundamon.png",
		"gs://bucket/jobs/job-1/images/cut_2.png",
	})
}

// TestVideoRequestBuilderReferenceImagesWithoutCharacterArt は、キャラクターが解決できない
// （未設定・未知ID）場合でも、カット自身のキーフレームが referenceImages として
// 使われることを検証します。video.CutReferenceImages が referenceImages の唯一の組み立て
// 規則で、リクエストの生成モード判定もこの同じリストを見ます。
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

// TestVideoRequestBuilderLastFrameNeedsModelSupport は、lastFrame（frames_to_video 補間）が
// 対応モデルのときだけリクエストに残ることを検証します。非対応モデルに送っても無視される
// だけなので、組み立て段階で落として実際に送られる内容と一致させます。
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

// TestVideoRequestBuilderFallsBackToCharacterSeed は、キーフレーム生成結果からシードが
// 得られない場合にキャラクターのシード（キーフレーム生成が使うのと同じ値）へ、それも
// 無ければレシピ全体のシードへフォールバックすることを検証します。
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
