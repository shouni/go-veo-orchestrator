package runner

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/shouni/go-gemini-client/gemini"
	"github.com/shouni/go-veo-orchestrator/ports"
)

// fakeScriptPrompt implements ports.ScriptPrompt and records the last mode/data it was
// called with.
type fakeScriptPrompt struct {
	prompt   string
	err      error
	lastMode string
	lastData *ports.TemplateData
}

func (f *fakeScriptPrompt) Build(mode string, data *ports.TemplateData) (string, error) {
	f.lastMode = mode
	f.lastData = data
	if f.err != nil {
		return "", f.err
	}
	return f.prompt, nil
}

// fakeContentGenerator implements gemini.ContentGenerator and records the model/prompt it
// was called with.
type fakeContentGenerator struct {
	resp       *gemini.Response
	err        error
	calls      int
	lastModel  string
	lastPrompt string
}

func (f *fakeContentGenerator) GenerateContent(_ context.Context, modelName string, prompt string) (*gemini.Response, error) {
	f.calls++
	f.lastModel = modelName
	f.lastPrompt = prompt
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

type failingContentReader struct {
	err error
}

func (r failingContentReader) Open(context.Context, string) (io.ReadCloser, error) {
	return nil, r.err
}

func TestVideoScriptRunner_Run(t *testing.T) {
	ctx := context.Background()

	t.Run("builds a video recipe from the AI response", func(t *testing.T) {
		reader := staticContentReader{content: []byte(`{"title":"music title","mood":"cinematic"}`)}
		promptBuilder := &fakeScriptPrompt{prompt: "built-prompt"}
		ai := &fakeContentGenerator{
			resp: &gemini.Response{Text: `{"project_title":"Generated","cuts":[{"cut_index":1,"duration_sec":5}]}`},
		}

		r := NewVideoScriptRunner(promptBuilder, ai, reader, "gemini-test-model")
		recipe, err := r.Run(ctx, "memory://source", "default")
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if recipe.ProjectTitle != "Generated" {
			t.Errorf("ProjectTitle = %q, want Generated", recipe.ProjectTitle)
		}
		if len(recipe.Cuts) != 1 {
			t.Fatalf("len(Cuts) = %d, want 1", len(recipe.Cuts))
		}
		if ai.lastModel != "gemini-test-model" {
			t.Errorf("model = %q, want gemini-test-model", ai.lastModel)
		}
		if promptBuilder.lastMode != "default" {
			t.Errorf("mode = %q, want default", promptBuilder.lastMode)
		}
		if promptBuilder.lastData == nil || promptBuilder.lastData.SourceRecipe == nil {
			t.Fatal("expected prompt builder to receive the parsed source recipe")
		}
		if promptBuilder.lastData.SourceRecipe.MusicRecipe.Title != "music title" {
			t.Errorf("source recipe title = %q, want music title", promptBuilder.lastData.SourceRecipe.MusicRecipe.Title)
		}
	})

	t.Run("wraps a source read failure", func(t *testing.T) {
		r := NewVideoScriptRunner(&fakeScriptPrompt{}, &fakeContentGenerator{}, failingContentReader{err: errors.New("boom")}, "model")

		if _, err := r.Run(ctx, "memory://source", "default"); err == nil {
			t.Fatal("expected error when source read fails")
		}
	})

	t.Run("errors when source has no recipe content", func(t *testing.T) {
		reader := staticContentReader{content: []byte("just some prose, no JSON here")}
		r := NewVideoScriptRunner(&fakeScriptPrompt{}, &fakeContentGenerator{}, reader, "model")

		if _, err := r.Run(ctx, "memory://source", "default"); err == nil {
			t.Fatal("expected error when source cannot be parsed as a recipe")
		}
	})

	t.Run("wraps a prompt build failure", func(t *testing.T) {
		reader := staticContentReader{content: []byte(`{"title":"t"}`)}
		r := NewVideoScriptRunner(&fakeScriptPrompt{err: errors.New("bad template")}, &fakeContentGenerator{}, reader, "model")

		if _, err := r.Run(ctx, "memory://source", "default"); err == nil {
			t.Fatal("expected error when prompt building fails")
		}
	})

	t.Run("wraps an AI call failure", func(t *testing.T) {
		reader := staticContentReader{content: []byte(`{"title":"t"}`)}
		ai := &fakeContentGenerator{err: errors.New("api down")}
		r := NewVideoScriptRunner(&fakeScriptPrompt{prompt: "p"}, ai, reader, "model")

		if _, err := r.Run(ctx, "memory://source", "default"); err == nil {
			t.Fatal("expected error when the AI call fails")
		}
	})

	t.Run("returns ErrInvalidAIResponse for unparsable AI output", func(t *testing.T) {
		reader := staticContentReader{content: []byte(`{"title":"t"}`)}
		ai := &fakeContentGenerator{resp: &gemini.Response{Text: `{"cuts": not-json}`}}
		r := NewVideoScriptRunner(&fakeScriptPrompt{prompt: "p"}, ai, reader, "model")

		_, err := r.Run(ctx, "memory://source", "default")
		if !errors.Is(err, ports.ErrInvalidAIResponse) {
			t.Fatalf("expected ErrInvalidAIResponse, got %v", err)
		}
	})
}

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
