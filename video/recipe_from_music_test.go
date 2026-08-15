package video

import (
	"testing"

	"github.com/shouni/go-gemini-client/music"
)

// TestNewVideoRecipeFromMusicKeepsEveryField は、音楽レシピの取り込みが深いコピーで
// 行われ、フィールドを取りこぼさないことを検証します。以前は消費者側の手書きコピーが
// Seed / AudioModel / ComposeMode を silently drop していました。
func TestNewVideoRecipeFromMusicKeepsEveryField(t *testing.T) {
	seed := int64(42)
	src := music.Recipe{
		Title:        "Song",
		Theme:        "theme",
		Mood:         "mood",
		Tempo:        128,
		Key:          "A minor",
		VocalProfile: "clear vocal",
		Instruments:  []string{"synth", "drums"},
		Sections: []music.Section{
			{Name: "Verse", Duration: 30, StartSeconds: 0, EndSeconds: 30, Prompt: "pulse"},
		},
		Lyrics: &music.LyricsDraft{Title: "Song", Lyrics: "words"},
		AIModels: music.AIModels{
			TextModel:   "gemini-x",
			AudioModel:  "lyria-x",
			LyricsMode:  "mode-l",
			ComposeMode: "mode-c",
			Seed:        &seed,
			Lang:        music.LangJapanese,
		},
	}

	vr := NewRecipeFromMusic(src)

	if vr.ProjectTitle != "Song" {
		t.Errorf("ProjectTitle = %q, want cross-filled title", vr.ProjectTitle)
	}
	got := vr.MusicRecipe
	if got.AudioModel != "lyria-x" || got.ComposeMode != "mode-c" || got.LyricsMode != "mode-l" {
		t.Errorf("AIModels dropped: %+v", got.AIModels)
	}
	if got.Seed == nil || *got.Seed != seed {
		t.Errorf("Seed dropped: %v", got.Seed)
	}
	if got.Key != "A minor" || got.VocalProfile != "clear vocal" {
		t.Errorf("Key/VocalProfile dropped: %+v", got)
	}
	if len(vr.Cuts) != 1 {
		t.Fatalf("cuts = %d, want section-derived cut", len(vr.Cuts))
	}

	// 深いコピーであること: 元を書き換えても取り込んだ側は変わらない。
	src.Instruments[0] = "changed"
	*src.Seed = 999
	if vr.MusicRecipe.Instruments[0] != "synth" {
		t.Error("Instruments are shared with the source (shallow copy)")
	}
	if *vr.MusicRecipe.Seed != 42 {
		t.Error("Seed pointer is shared with the source (shallow copy)")
	}
}

func TestCutsFillHelpers(t *testing.T) {
	cuts := Cuts{
		{CutIndex: 1},
		{CutIndex: 2, AudioSync: AudioSync{AudioReference: "gs://audio/explicit.mp3"}, CharacterID: "metan"},
		{CutIndex: 5},
	}

	cuts.FillAudioReference("gs://audio/task.mp3")
	cuts.FillCharacterID("zundamon")

	if cuts[0].AudioReference != "gs://audio/task.mp3" || cuts[2].AudioReference != "gs://audio/task.mp3" {
		t.Errorf("unset cuts should receive the task audio: %+v", cuts)
	}
	if cuts[1].AudioReference != "gs://audio/explicit.mp3" {
		t.Errorf("explicit audio reference must not be overwritten: %q", cuts[1].AudioReference)
	}
	if cuts[0].CharacterID != "zundamon" || cuts[1].CharacterID != "metan" {
		t.Errorf("character fill wrong: %+v", cuts)
	}

	if got := cuts.IndexOf(5); got != 2 {
		t.Errorf("IndexOf(5) = %d, want 2", got)
	}
	if got := cuts.IndexOf(99); got != -1 {
		t.Errorf("IndexOf(99) = %d, want -1", got)
	}
}
