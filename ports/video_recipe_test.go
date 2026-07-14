package ports

import "testing"

func TestVideoRecipeNormalizeBuildsCutsFromVariableSections(t *testing.T) {
	recipe := &VideoRecipe{
		MusicRecipe: MusicRecipe{
			Title: "碧き残影、一瞬の奇跡 〜黒き疾風の叙事詩〜",
			Mood:  "Epic Symphonic Fantasy Rock Ballad, Emotional and Melancholic",
			Tempo: 72,
			Sections: []Section{
				{
					Name:     "Verse",
					Duration: 40,
					Prompt:   "[Silent Awakening] Focus strictly on the first lyrics block marked [Verse].",
				},
				{
					Name:     "Chorus",
					Duration: 45,
					Prompt:   "[Emotional Outburst & High-Voltage Peak] Focus on the lyrics marked [Chorus].",
				},
				{
					Name:     "Chorus 2",
					Duration: 55,
					Prompt:   "[The Final Peak & Ultimate Triumphant Finale] Focus on the final lyrics marked [Chorus 2].",
				},
			},
		},
	}

	recipe.Normalize()

	if recipe.ProjectTitle != "碧き残影、一瞬の奇跡 〜黒き疾風の叙事詩〜" {
		t.Errorf("ProjectTitle = %q", recipe.ProjectTitle)
	}
	if recipe.MusicRecipe.Tempo != 72 {
		t.Errorf("Tempo = %d, want 72", recipe.MusicRecipe.Tempo)
	}
	if recipe.MusicRecipe.Mood != "Epic Symphonic Fantasy Rock Ballad, Emotional and Melancholic" {
		t.Errorf("Mood = %q", recipe.MusicRecipe.Mood)
	}
	if len(recipe.MusicRecipe.Sections) != 3 {
		t.Fatalf("len(MusicRecipe.Sections) = %d, want 3", len(recipe.MusicRecipe.Sections))
	}
	if len(recipe.Cuts) != 3 {
		t.Fatalf("len(Cuts) = %d, want 3", len(recipe.Cuts))
	}
	if recipe.Cuts[2].StartSec != 85 || recipe.Cuts[2].EndSec != 140 {
		t.Errorf("third cut range = %.1f-%.1f, want 85-140", recipe.Cuts[2].StartSec, recipe.Cuts[2].EndSec)
	}
	if recipe.Cuts[0].VisualAnchor != "Verse" {
		t.Errorf("first cut VisualAnchor = %q, want Verse", recipe.Cuts[0].VisualAnchor)
	}
	if recipe.Cuts[0].SectionIndex != 1 || recipe.Cuts[1].SectionIndex != 2 || recipe.Cuts[2].SectionIndex != 3 {
		t.Errorf("SectionIndex = %d, %d, %d, want 1, 2, 3",
			recipe.Cuts[0].SectionIndex, recipe.Cuts[1].SectionIndex, recipe.Cuts[2].SectionIndex)
	}
}

func TestVideoRecipeNormalizeKeepsMusicRecipeSlicesIsolatedFromCuts(t *testing.T) {
	recipe := &VideoRecipe{
		MusicRecipe: MusicRecipe{
			Instruments: []string{"piano"},
			Sections: []Section{
				{
					Name:     "Verse",
					Duration: 30,
				},
			},
		},
	}

	recipe.Normalize()

	recipe.Cuts[0].VisualAnchor = "Chorus"
	if recipe.MusicRecipe.Sections[0].Name != "Verse" {
		t.Fatalf("MusicRecipe.Sections should remain unchanged after mutating Cuts")
	}
}

func TestVideoRecipeNormalizeUsesProjectTitleAsMusicTitleFallback(t *testing.T) {
	recipe := &VideoRecipe{
		ProjectTitle: "fallback title",
		MusicRecipe: MusicRecipe{
			Sections: []Section{
				{
					Name:     "Verse",
					Duration: 30,
				},
			},
		},
	}

	recipe.Normalize()

	if recipe.MusicRecipe.Title != "fallback title" {
		t.Fatalf("MusicRecipe.Title = %q, want fallback title", recipe.MusicRecipe.Title)
	}
}

func TestVideoRecipeNormalizeUsesMusicTitleAsProjectTitleFallback(t *testing.T) {
	recipe := &VideoRecipe{
		MusicRecipe: MusicRecipe{
			Title: "music title",
			Sections: []Section{
				{
					Name:     "Verse",
					Duration: 30,
				},
			},
		},
	}

	recipe.Normalize()

	if recipe.ProjectTitle != "music title" {
		t.Fatalf("ProjectTitle = %q, want music title", recipe.ProjectTitle)
	}
}

func TestVideoRecipeNormalizeBuildsCutsFromMusicRecipeSections(t *testing.T) {
	recipe := &VideoRecipe{
		MusicRecipe: MusicRecipe{
			Sections: []Section{
				{
					Name:         "Bridge",
					StartSeconds: 12,
					EndSeconds:   34,
					Prompt:       "bridge prompt",
				},
			},
		},
	}

	recipe.Normalize()

	if len(recipe.Cuts) != 1 {
		t.Fatalf("len(Cuts) = %d, want 1", len(recipe.Cuts))
	}
	if recipe.Cuts[0].DurationSec != 22 {
		t.Fatalf("DurationSec = %.1f, want 22", recipe.Cuts[0].DurationSec)
	}
	if recipe.Cuts[0].AudioCue != "bridge prompt" {
		t.Fatalf("AudioCue = %q, want bridge prompt", recipe.Cuts[0].AudioCue)
	}
}

// TestVideoRecipeNormalizeDerivesSectionIndexFromStartSec verifies the fallback path: when an
// explicit Cut list is provided without SectionIndex (e.g. scene-split sub-cuts authored before
// this field existed), Normalize derives it from StartSec against the section time ranges,
// instead of leaving it at the zero value.
func TestVideoRecipeNormalizeDerivesSectionIndexFromStartSec(t *testing.T) {
	recipe := &VideoRecipe{
		MusicRecipe: MusicRecipe{
			Sections: []Section{
				{Name: "Verse", StartSeconds: 0, EndSeconds: 30},
				{Name: "Chorus", StartSeconds: 30, EndSeconds: 60},
			},
		},
		Cuts: []Cut{
			{VisualAnchor: "verse scene 1", AudioSync: AudioSync{StartSec: 0, DurationSec: 15}},
			{VisualAnchor: "verse scene 2", AudioSync: AudioSync{StartSec: 15, DurationSec: 15}},
			{VisualAnchor: "chorus scene 1", AudioSync: AudioSync{StartSec: 30, DurationSec: 30}},
		},
	}

	recipe.Normalize()

	if recipe.Cuts[0].SectionIndex != 1 || recipe.Cuts[1].SectionIndex != 1 {
		t.Errorf("verse cuts SectionIndex = %d, %d, want 1, 1", recipe.Cuts[0].SectionIndex, recipe.Cuts[1].SectionIndex)
	}
	if recipe.Cuts[2].SectionIndex != 2 {
		t.Errorf("chorus cut SectionIndex = %d, want 2", recipe.Cuts[2].SectionIndex)
	}
}

// TestVideoRecipeNormalizeKeepsExplicitSectionIndex verifies that an explicitly set
// SectionIndex is never overwritten by the StartSec-based fallback derivation.
func TestVideoRecipeNormalizeKeepsExplicitSectionIndex(t *testing.T) {
	recipe := &VideoRecipe{
		MusicRecipe: MusicRecipe{
			Sections: []Section{
				{Name: "Verse", StartSeconds: 0, EndSeconds: 30},
			},
		},
		Cuts: []Cut{
			{
				SectionIndex: 7,
				VisualAnchor: "explicit section",
				AudioSync:    AudioSync{StartSec: 0, DurationSec: 15},
			},
		},
	}

	recipe.Normalize()

	if recipe.Cuts[0].SectionIndex != 7 {
		t.Fatalf("SectionIndex = %d, want 7 (explicit value preserved)", recipe.Cuts[0].SectionIndex)
	}
}

func TestVideoRecipeNormalizeDoesNotOverwriteExplicitCuts(t *testing.T) {
	recipe := &VideoRecipe{
		MusicRecipe: MusicRecipe{
			Sections: []Section{
				{
					Name:     "Verse",
					Duration: 30,
				},
			},
		},
		Cuts: []Cut{
			{
				VisualAnchor: "explicit cut",
				AudioSync:    AudioSync{DurationSec: 8},
			},
		},
	}

	recipe.Normalize()

	if len(recipe.Cuts) != 1 {
		t.Fatalf("len(Cuts) = %d, want 1", len(recipe.Cuts))
	}
	if recipe.Cuts[0].VisualAnchor != "explicit cut" {
		t.Fatalf("VisualAnchor = %q, want explicit cut", recipe.Cuts[0].VisualAnchor)
	}
}
