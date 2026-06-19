package runner

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestReadContentRemovesInvalidUTF8Anywhere(t *testing.T) {
	r := &VideoScriptRunner{
		reader: staticContentReader{content: []byte{'a', 0xff, 'b'}},
	}

	got, err := r.readContent(context.Background(), "memory://invalid-utf8")
	if err != nil {
		t.Fatalf("readContent() error = %v", err)
	}
	if got != "ab" {
		t.Fatalf("readContent() = %q, want %q", got, "ab")
	}
}

func TestExtractJSONStringSupportsArrayRoot(t *testing.T) {
	raw := "prefix [{\"id\":1}] suffix"

	got := extractJSONString(raw)
	if got != "[{\"id\":1}]" {
		t.Fatalf("extractJSONString() = %q", got)
	}
}

func TestExtractJSONStringSupportsObjectRoot(t *testing.T) {
	raw := "prefix {\"id\":1} suffix"

	got := extractJSONString(raw)
	if got != "{\"id\":1}" {
		t.Fatalf("extractJSONString() = %q", got)
	}
}

func TestParseSourceRecipeSupportsVideoRecipeRoot(t *testing.T) {
	raw := `{
		"project_title": "video project",
		"music_recipe": {
			"title": "music title",
			"sections": [
				{
					"name": "Intro",
					"duration_seconds": 5,
					"prompt": "quiet intro"
				}
			]
		},
		"cuts": []
	}`

	recipe, err := parseSourceRecipe(raw)
	if err != nil {
		t.Fatalf("parseSourceRecipe() error = %v", err)
	}
	if recipe == nil {
		t.Fatal("parseSourceRecipe() = nil")
	}
	if recipe.ProjectTitle != "video project" {
		t.Fatalf("ProjectTitle = %q, want video project", recipe.ProjectTitle)
	}
	if len(recipe.Cuts) != 1 {
		t.Fatalf("len(Cuts) = %d, want 1", len(recipe.Cuts))
	}
}

func TestParseSourceRecipeSupportsMusicRecipeRoot(t *testing.T) {
	raw := `{
		"title": "music title",
		"mood": "cinematic",
		"sections": [
			{
				"name": "Chorus",
				"duration_seconds": 8,
				"prompt": "big chorus"
			}
		]
	}`

	recipe, err := parseSourceRecipe(raw)
	if err != nil {
		t.Fatalf("parseSourceRecipe() error = %v", err)
	}
	if recipe == nil {
		t.Fatal("parseSourceRecipe() = nil")
	}
	if recipe.MusicRecipe.Title != "music title" {
		t.Fatalf("MusicRecipe.Title = %q, want music title", recipe.MusicRecipe.Title)
	}
	if recipe.ProjectTitle != "music title" {
		t.Fatalf("ProjectTitle = %q, want music title", recipe.ProjectTitle)
	}
	if len(recipe.Cuts) != 1 {
		t.Fatalf("len(Cuts) = %d, want 1", len(recipe.Cuts))
	}
	if recipe.Cuts[0].AudioCue != "big chorus" {
		t.Fatalf("AudioCue = %q, want big chorus", recipe.Cuts[0].AudioCue)
	}
}

func TestParseSourceRecipeIgnoresNonRecipeJSON(t *testing.T) {
	recipe, err := parseSourceRecipe(`{"id": 1}`)
	if err != nil {
		t.Fatalf("parseSourceRecipe() error = %v", err)
	}
	if recipe != nil {
		t.Fatalf("parseSourceRecipe() = %#v, want nil", recipe)
	}
}

type staticContentReader struct {
	content []byte
}

func (r staticContentReader) Open(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(string(r.content))), nil
}
