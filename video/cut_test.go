package video

import (
	"reflect"
	"testing"

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
	if got := cuts.UniqueCharacterIDs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("UniqueCharacterIDs() = %v, want %v", got, want)
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
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("CutReferenceImages() = %v, want %v", got, tt.want)
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
		{"explicit duration wins", Cut{AudioSync: AudioSync{DurationSec: 8, StartSec: 10, EndSec: 30}}, 8},
		{"derived from the timeline", Cut{AudioSync: AudioSync{StartSec: 10, EndSec: 16}}, 6},
		{"neither set", Cut{}, 0},
		{"end before start", Cut{AudioSync: AudioSync{StartSec: 20, EndSec: 10}}, 0},
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
		KeyframeResult: KeyframeResult{KeyframeReference: "gs://bucket/kf.png"},
		Result:         Result{VideoID: "gs://bucket/cut.mp4", VideoURL: "gs://bucket/cut.mp4", Status: CutStatusGenerated},
		ChainControl:   ChainControl{IsChainStart: true, IsSectionStart: true},
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
				{CharacterID: "zundamon", KeyframeResult: kf("gs://b.png"), ChainControl: ChainControl{IsSectionStart: true}},
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
