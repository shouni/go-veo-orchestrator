// Package keyframe provides the logic that generates a video's keyframe images from
// cut information and character definitions. Reference images are handed to
// genai-kit's imagegen as gs:// URIs, unchanged, so this package's only job is to
// assemble one cut's reference URL and prompt and send it once. Concurrency belongs
// to runner.CutKeyframeRunner, and the call interval and per-call time limit to
// workflow's callGuard.
package keyframe

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/shouni/genai-kit/gemini"
	"github.com/shouni/genai-kit/imagegen"
	characterkit "github.com/shouni/go-character-kit/character"

	"github.com/shouni/go-veo-orchestrator/ports"
	"github.com/shouni/go-veo-orchestrator/video"
)

// Generator generates cut keyframes one at a time while keeping characters consistent.
type Generator struct {
	characters     *characterkit.Characters
	generator      imagegen.Generator
	pb             ports.KeyframePrompt
	model          string
	aspectRatio    string
	imageSize      string
	negativePrompt string
}

type keyframeTask struct {
	index int
	// total is the number of keyframes generated in this batch (for progress logs).
	total int
	cut   video.Cut
}

// NewGenerator initializes a new Generator.
func NewGenerator(
	characters *characterkit.Characters,
	generator imagegen.Generator,
	pb ports.KeyframePrompt,
	model string,
	opts ...Option,
) *Generator {
	g := &Generator{
		characters: characters,
		generator:  generator,
		pb:         pb,
		model:      model,
	}

	for _, opt := range opts {
		opt(g)
	}

	return g
}

// GenerateCut generates the keyframe for one cut. It does not run anything in
// parallel (so the caller can save each image as it comes — see
// CutKeyframeRunner.GenerateAndSave). index / total are for progress logs (1-based)
// and do not affect generation itself.
func (g *Generator) GenerateCut(ctx context.Context, cut video.Cut, index, total int) (*video.KeyframeImage, error) {
	return g.generateCutKeyframe(ctx, keyframeTask{index: index - 1, total: total, cut: cut})
}

func (g *Generator) generateCutKeyframe(ctx context.Context, task keyframeTask) (*video.KeyframeImage, error) {
	char := g.characterForCut(task.cut)
	if char == nil {
		return nil, fmt.Errorf("cut %d: no character found for character ID '%s'", task.cut.CutIndex, task.cut.CharacterID)
	}

	req := g.buildImageRequest(task.cut, char)
	logger := newKeyframeLogger(task, char, req.Images)

	// Put the progress in the message text too. The attributes
	// (keyframe_index / keyframe_total) carry the same values, but Cloud Logging's
	// collapsed view shows only the message, and for work taking minutes per image,
	// progress cannot be judged without reading "image n of m".
	progress := fmt.Sprintf("(%d/%d)", task.index+1, task.total)
	return g.runImageGeneration(ctx, imageRun{
		req:         req,
		logger:      logger,
		startLog:    "Starting keyframe generation " + progress,
		completeLog: "Keyframe generation completed " + progress,
		actionJP:    "keyframe generation",
		cutIndex:    task.index + 1,
		characterID: char.ID,
	})
}

// imageRun describes one call to runImageGeneration.
//
// As positional arguments, three unrelated strings (startLog / completeLog /
// actionJP) would sit side by side and still compile when mixed up, so they come in
// a struct (the same reason as go-comic-kit's *RunnerArgs).
type imageRun struct {
	req    imagegen.Request
	logger *slog.Logger
	// startLog / completeLog are the message texts at start and completion (the elapsed time is appended to the completion one).
	startLog, completeLog string
	// actionJP is the action name embedded in the error text ("keyframe generation", etc.).
	// The name dates from when the text was Japanese.
	actionJP string
	// cutIndex / characterID are the identifiers embedded in the error text.
	cutIndex    int
	characterID string
}

// runImageGeneration wraps one Generate call in start/completion logs and error
// text. It is shared by generateCutKeyframe and EditCut.
func (g *Generator) runImageGeneration(ctx context.Context, run imageRun) (*video.KeyframeImage, error) {
	run.logger.Info(run.startLog)
	startTime := time.Now()

	resp, err := g.generator.Generate(ctx, run.req)
	if err != nil {
		return nil, fmt.Errorf("cut %d (character_id: %s): %s failed: %w",
			run.cutIndex, run.characterID, run.actionJP, err)
	}

	// Put the elapsed time in the message too. When generation is slow, a list of per-image measurements alone gives a lead.
	elapsed := time.Since(startTime).Round(time.Second)
	run.logger.Info(fmt.Sprintf("%s %s", run.completeLog, elapsed), "duration", elapsed)

	return keyframeImageFrom(resp), nil
}

// keyframeImageFrom maps an imagegen response onto the library's own type. It is
// the boundary that keeps imagegen's types off the public API; only this package
// knows both.
func keyframeImageFrom(resp *imagegen.Response) *video.KeyframeImage {
	if resp == nil {
		return nil
	}
	return &video.KeyframeImage{
		Data:     resp.Data,
		MimeType: resp.MIMEType,
		UsedSeed: resp.UsedSeed,
		Model:    resp.Model,
		Prompt:   resp.Prompt,
	}
}

// EditCut edits an existing keyframe image (with cut.KeyframeReference as the
// source image) according to a text instruction. It keeps the composition, pose and
// background and changes only what editPrompt specifies.
//
// It uses the same conversational image model as GenerateCut rather than a
// dedicated editing API: Gemini's multimodal image models (the Nano Banana family)
// can edit an input image through ordinary generateContent, and Vertex AI's Imagen
// mask-editing models have no successor.
func (g *Generator) EditCut(ctx context.Context, cut video.Cut, editPrompt string) (*video.KeyframeImage, error) {
	if cut.KeyframeReference == "" {
		return nil, fmt.Errorf("cut %d: %w", cut.CutIndex, ports.ErrNoKeyframeToEdit)
	}

	char := g.characterForCut(cut)
	if char == nil {
		return nil, fmt.Errorf("cut %d: no character found for character ID '%s'", cut.CutIndex, cut.CharacterID)
	}

	userPrompt, systemPrompt := g.pb.BuildEdit(cut, char, editPrompt)
	req := g.buildRequest(userPrompt, systemPrompt, char.Seed, cut.KeyframeReference)

	logger := slog.With(
		"cut_index", cut.CutIndex,
		"character_id", char.ID,
		"character_name", char.Name,
	)

	return g.runImageGeneration(ctx, imageRun{
		req:         req,
		logger:      logger,
		startLog:    "Starting keyframe edit",
		completeLog: "Keyframe edit completed",
		actionJP:    "keyframe editing",
		cutIndex:    cut.CutIndex,
		characterID: char.ID,
	})
}

// characterForCut resolves the character for a cut. When cut.CharacterID is unset
// or an unknown ID, it falls back to the default character.
func (g *Generator) characterForCut(cut video.Cut) *characterkit.Character {
	return g.characters.GetCharacterWithDefault(cut.CharacterID)
}

func (g *Generator) buildImageRequest(cut video.Cut, char *characterkit.Character) imagegen.Request {
	userPrompt, systemPrompt := g.pb.BuildCut(cut, char)
	// When the character's reference image has a different aspect ratio from what
	// is generated (e.g. a landscape three-pose sheet used as the reference for a
	// portrait keyframe), details such as colors, prop placement and hairstyle tend to
	// wobble from one generation to the next, so a reference image matching
	// g.aspectRatio (ReferenceURLs) is preferred when one exists.
	referenceURL := char.ReferenceURLFor(g.aspectRatio)

	return g.buildRequest(userPrompt, systemPrompt, char.Seed, referenceURL)
}

// buildRequest assembles the request shared by buildImageRequest and EditCut. The
// only difference between generating anew and editing an existing image is the
// reference image.
//
// An empty referenceURL is not dropped. imagegen leaves out elements with no
// reference target when sending, so "this character has no reference image" can be
// expressed as-is.
//
// The generation parameters are written as a GenerateOptions literal with its type
// name because fields promoted through embedding cannot be used as keys in a
// composite literal (the IDE's hint that the type name can be omitted is wrong;
// omitting it gives "missing type in composite literal").
func (g *Generator) buildRequest(prompt, systemPrompt string, seed *int64, referenceURL string) imagegen.Request {
	return imagegen.Request{
		Model:          g.model,
		Prompt:         prompt,
		NegativePrompt: g.negativePrompt,
		Images:         []string{referenceURL},
		GenerateOptions: gemini.GenerateOptions{
			SystemPrompt: systemPrompt,
			AspectRatio:  g.aspectRatio,
			ImageSize:    g.imageSize,
			Seed:         seed,
		},
	}
}

func newKeyframeLogger(task keyframeTask, char *characterkit.Character, images []string) *slog.Logger {
	referenceURL := ""
	if len(images) > 0 {
		referenceURL = images[0]
	}
	return slog.With(
		"keyframe_index", task.index+1,
		"keyframe_total", task.total,
		"character_id", char.ID,
		"character_name", char.Name,
		"seed", char.Seed,
		"has_reference", referenceURL != "",
	)
}
