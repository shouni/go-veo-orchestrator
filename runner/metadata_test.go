package runner

import (
	"encoding/json"
	"testing"

	"github.com/shouni/go-veo-orchestrator/ports"
)

func TestBuildRecipeMetadataDoesNotNormalizeRecipe(t *testing.T) {
	recipe := &ports.VideoRecipe{
		ProjectTitle: "fallback title",
		Cuts: []ports.Cut{
			{
				DurationSec:  8,
				VisualAnchor: "explicit cut",
			},
		},
	}

	data, err := buildRecipeMetadata(recipe)
	if err != nil {
		t.Fatalf("buildRecipeMetadata() error = %v", err)
	}

	var got ports.VideoRecipe
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("metadata JSON should be valid: %v", err)
	}

	if recipe.MusicRecipe.Title != "" {
		t.Fatalf("MusicRecipe.Title was normalized to %q", recipe.MusicRecipe.Title)
	}
	if recipe.Cuts[0].CutIndex != 0 {
		t.Fatalf("CutIndex was normalized to %d", recipe.Cuts[0].CutIndex)
	}
	if recipe.Cuts[0].Status != "" {
		t.Fatalf("Status was normalized to %q", recipe.Cuts[0].Status)
	}
}
