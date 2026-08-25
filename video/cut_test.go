package video

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	characterkit "github.com/shouni/go-character-kit/character"
)

func testCharacters(t *testing.T) *characterkit.Characters {
	t.Helper()
	chars, err := characterkit.NewCharacters([]characterkit.Character{
		{
			ID:           "zundamon",
			Name:         "Zundamon",
			ReferenceURL: "gs://bucket/characters/zundamon.png",
			VisualCues:   []string{"green hair"},
		},
	})
	if err != nil {
		t.Fatalf("NewCharacters() error = %v", err)
	}
	return chars
}

func TestCutsUniqueCharacterIDs(t *testing.T) {
	cuts := Cuts{
		{CharacterID: "metan"},
		{CharacterID: "zundamon"},
		{CharacterID: "metan"},
		{CharacterID: ""},
	}
	want := []string{"metan", "zundamon"}
	if diff := cmp.Diff(want, cuts.UniqueCharacterIDs()); diff != "" {
		t.Fatalf("UniqueCharacterIDs() mismatch (-want +got):\n%s", diff)
	}
}

// TestCutReferenceImages verifies the single referenceImages rule: character art first, then
// the cut's own keyframe, blanks skipped, and nil when the cut has neither (so the adapter
// falls back to image_to_video).
func TestCutReferenceImages(t *testing.T) {
	chars := testCharacters(t)
	tests := []struct {
		name       string
		cut        Cut
		characters *characterkit.Characters
		want       []string
	}{
		{
			name:       "character art and keyframe",
			cut:        Cut{CharacterID: "zundamon", KeyframeResult: KeyframeResult{KeyframeReference: "gs://bucket/kf.png"}},
			characters: chars,
			want:       []string{"gs://bucket/characters/zundamon.png", "gs://bucket/kf.png"},
		},
		{
			name:       "keyframe only when the character is unknown",
			cut:        Cut{CharacterID: "unknown", KeyframeResult: KeyframeResult{KeyframeReference: "gs://bucket/kf.png"}},
			characters: chars,
			want:       []string{"gs://bucket/kf.png"},
		},
		{
			name:       "keyframe only when characters are not configured",
			cut:        Cut{CharacterID: "zundamon", KeyframeResult: KeyframeResult{KeyframeReference: "gs://bucket/kf.png"}},
			characters: nil,
			want:       []string{"gs://bucket/kf.png"},
		},
		{
			name:       "character art only",
			cut:        Cut{CharacterID: "zundamon"},
			characters: chars,
			want:       []string{"gs://bucket/characters/zundamon.png"},
		},
		{
			name:       "blank keyframe is skipped",
			cut:        Cut{CharacterID: "unknown", KeyframeResult: KeyframeResult{KeyframeReference: "   "}},
			characters: chars,
			want:       nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CutReferenceImages(tt.cut, tt.characters)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatalf("CutReferenceImages() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestCutEffectiveDurationSec(t *testing.T) {
	tests := []struct {
		name string
		cut  Cut
		want float64
	}{
		{"explicit duration wins", Cut{DurationSec: 8, StartSec: 10, EndSec: 30}, 8},
		{"derived from the timeline", Cut{StartSec: 10, EndSec: 16}, 6},
		{"neither set", Cut{}, 0},
		{"end before start", Cut{StartSec: 20, EndSec: 10}, 0},
	}
	for _, tt := range tests {
		if got := tt.cut.EffectiveDurationSec(); got != tt.want {
			t.Errorf("%s: EffectiveDurationSec() = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// TestCutResetGeneration verifies keepKeyframe decides whether a reset cut keeps the keyframe
// it was generated from: false means a fresh scene keyframe will be generated for it, true
// means the caller reuses the stored one.
func TestCutResetGeneration(t *testing.T) {
	base := Cut{
		KeyframeReference: "gs://bucket/kf.png",
		VideoID:           "gs://bucket/cut.mp4", VideoURL: "gs://bucket/cut.mp4", Status: CutStatusGenerated,
		IsChainStart: true, IsSectionStart: true,
	}

	dropped := base
	dropped.ResetGeneration(false)
	if dropped.Status != CutStatusPending || dropped.VideoID != "" || dropped.VideoURL != "" || dropped.IsChainStart {
		t.Fatalf("ResetGeneration(false) left generation state: %+v", dropped)
	}
	if dropped.KeyframeReference != "" || dropped.IsSectionStart {
		t.Fatalf("ResetGeneration(false) should drop the keyframe and section flag: %+v", dropped)
	}

	kept := base
	kept.ResetGeneration(true)
	if kept.Status != CutStatusPending || kept.VideoID != "" || kept.VideoURL != "" || kept.IsChainStart {
		t.Fatalf("ResetGeneration(true) left generation state: %+v", kept)
	}
	if kept.KeyframeReference != "gs://bucket/kf.png" || !kept.IsSectionStart {
		t.Fatalf("ResetGeneration(true) should keep the keyframe and section flag: %+v", kept)
	}
}

// TestCutsNextLastFrameReference verifies each guard on frames_to_video interpolation: no next
// cut, no keyframe, a deliberate section change, a character switch (which would morph one
// character into another mid-cut), and a shared keyframe (which would freeze the motion).
func TestCutsNextLastFrameReference(t *testing.T) {
	kf := func(ref string) KeyframeResult { return KeyframeResult{KeyframeReference: ref} }
	tests := []struct {
		name string
		cuts Cuts
		i    int
		want string
	}{
		{
			name: "next cut keyframe",
			cuts: Cuts{
				{CharacterID: "zundamon", KeyframeResult: kf("gs://a.png")},
				{CharacterID: "zundamon", KeyframeResult: kf("gs://b.png")},
			},
			want: "gs://b.png",
		},
		{
			name: "character id comparison ignores case",
			cuts: Cuts{
				{CharacterID: "Zundamon", KeyframeResult: kf("gs://a.png")},
				{CharacterID: "zundamon", KeyframeResult: kf("gs://b.png")},
			},
			want: "gs://b.png",
		},
		{
			name: "last cut has no next",
			cuts: Cuts{{CharacterID: "zundamon", KeyframeResult: kf("gs://a.png")}},
			want: "",
		},
		{
			name: "next cut has no keyframe",
			cuts: Cuts{
				{CharacterID: "zundamon", KeyframeResult: kf("gs://a.png")},
				{CharacterID: "zundamon"},
			},
			want: "",
		},
		{
			name: "next cut starts a section",
			cuts: Cuts{
				{CharacterID: "zundamon", KeyframeResult: kf("gs://a.png")},
				{CharacterID: "zundamon", KeyframeResult: kf("gs://b.png"), IsSectionStart: true},
			},
			want: "",
		},
		{
			name: "different character",
			cuts: Cuts{
				{CharacterID: "zundamon", KeyframeResult: kf("gs://a.png")},
				{CharacterID: "metan", KeyframeResult: kf("gs://b.png")},
			},
			want: "",
		},
		{
			name: "shared keyframe",
			cuts: Cuts{
				{CharacterID: "zundamon", KeyframeResult: kf("gs://a.png")},
				{CharacterID: "zundamon", KeyframeResult: kf("gs://a.png")},
			},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cuts.NextLastFrameReference(tt.i); got != tt.want {
				t.Fatalf("NextLastFrameReference(%d) = %q, want %q", tt.i, got, tt.want)
			}
		})
	}

	// 範囲外のインデックスは panic せず空文字を返す。
	cuts := Cuts{{KeyframeResult: kf("gs://a.png")}}
	if got := cuts.NextLastFrameReference(-1); got != "" {
		t.Fatalf("NextLastFrameReference(-1) = %q, want empty", got)
	}
	if got := cuts.NextLastFrameReference(99); got != "" {
		t.Fatalf("NextLastFrameReference(99) = %q, want empty", got)
	}
}

// TestCutReferenceImagesUsesAspectRatioVariant pins that the character art sent to Veo as a
// referenceImage follows the cut's aspect ratio, the same way keyframe.Generator picks the
// character reference for the still image. When this used the plain ReferenceURL, a 9:16 job
// sent Veo a ratio-matched keyframe next to a 16:9 character sheet in the same request.
func TestCutReferenceImagesUsesAspectRatioVariant(t *testing.T) {
	chars, err := characterkit.NewCharacters([]characterkit.Character{
		{
			ID:           "tsumugi",
			Name:         "Tsumugi",
			ReferenceURL: "gs://bucket/characters/tsumugi-16x9.png",
			ReferenceURLs: map[string]string{
				"9:16": "gs://bucket/characters/tsumugi-9x16.png",
			},
			VisualCues: []string{"blonde hair"},
		},
	})
	if err != nil {
		t.Fatalf("NewCharacters() error = %v", err)
	}

	tests := []struct {
		name        string
		aspectRatio string
		want        string
	}{
		{"matching entry wins", "9:16", "gs://bucket/characters/tsumugi-9x16.png"},
		{"falls back when no entry matches", "1:1", "gs://bucket/characters/tsumugi-16x9.png"},
		{"falls back when the cut has no ratio", "", "gs://bucket/characters/tsumugi-16x9.png"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cut := Cut{CharacterID: "tsumugi", AspectRatio: tt.aspectRatio}
			got := CutReferenceImages(cut, chars)
			if diff := cmp.Diff([]string{tt.want}, got); diff != "" {
				t.Errorf("CutReferenceImages() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
