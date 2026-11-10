package ports

import (
	"context"

	characterkit "github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-veo-orchestrator/video"
)

// TemplateData is the structured input passed to the script generation prompt template.
type TemplateData struct {
	SourceRecipe *video.Recipe
}

// ScriptPrompt is the contract for building the script generation AI prompt.
type ScriptPrompt interface {
	Build(mode string, data *TemplateData) (string, error)
}

// KeyframePrompt is the contract for building prompts for a cut's keyframe image generation AI.
type KeyframePrompt interface {
	BuildCut(cut video.Cut, char *characterkit.Character) (userPrompt string, systemPrompt string)
	// BuildEdit builds the prompt for editing an existing keyframe image with
	// editPrompt. It reinforces character identity and stylistic consistency
	// (art style, negative prompt) the same way BuildCut does.
	BuildEdit(cut video.Cut, char *characterkit.Character, editPrompt string) (userPrompt string, systemPrompt string)
}

// CutImageGenerator generates the keyframe image for one cut.
//
// One image at a time is deliberate: it lets the caller (CutKeyframeRunner)
// save each image the moment it is produced. For what a batch shape would
// lose, see the comment on CutKeyframeRunner.GenerateAndSave. Running in
// parallel, keeping the order, and numbering index / total ("image n of m",
// for progress logs) are the caller's job.
type CutImageGenerator interface {
	GenerateCut(ctx context.Context, cut video.Cut, index, total int) (*video.KeyframeImage, error)
}
