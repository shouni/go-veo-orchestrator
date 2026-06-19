package keyframe

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	characterkit "github.com/shouni/go-character-kit/character"
	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"

	"github.com/shouni/go-veo-orchestrator/ports"
)

// negativeKeyframePrompt は単体カットのキーフレームで「文字」や「フキダシ」を排除するための指定です。
const negativeKeyframePrompt = "speech bubble, dialogue balloon, text, alphabet, letters, words, signatures, watermark, username, low quality, distorted, bad anatomy, monochrome, black and white, greyscale"

// Generator は、キャラクターの一貫性を保ちながら並列で複数カットのキーフレームを生成します。
type Generator struct {
	composer       *Composer
	generator      ImageGenerator
	pb             ports.KeyframePrompt
	model          string
	limiter        *rate.Limiter
	maxConcurrency int
	rateInterval   time.Duration
	rateBurst      int
}

type ImageGenerator interface {
	GenerateSingleImage(ctx context.Context, req imagePorts.SingleImageRequest) (*imagePorts.ImageResponse, error)
}

type keyframeTask struct {
	index int
	cut   ports.Cut
}

// NewGenerator は Generator の新しいインスタンスを初期化します。
func NewGenerator(
	composer *Composer,
	generator ImageGenerator,
	pb ports.KeyframePrompt,
	model string,
	opts ...Option,
) *Generator {
	g := &Generator{
		composer:       composer,
		generator:      generator,
		pb:             pb,
		model:          model,
		maxConcurrency: ports.DefaultMaxConcurrency,
		rateInterval:   defaultRateInterval,
		rateBurst:      defaultRateBurst,
	}

	for _, opt := range opts {
		opt(g)
	}

	g.limiter = rate.NewLimiter(rate.Every(g.rateInterval), g.rateBurst)

	return g
}

// Execute は、errgroupの制限機能を使用して同時実行数を制限しながらカットを並列生成します。
func (g *Generator) Execute(ctx context.Context, cuts []ports.Cut) ([]*imagePorts.ImageResponse, error) {
	if len(cuts) == 0 {
		return nil, nil
	}

	if err := g.composer.PrepareCharacterResources(ctx, cuts); err != nil {
		return nil, fmt.Errorf("prepare character resources: %w", err)
	}

	images := make([]*imagePorts.ImageResponse, len(cuts))
	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(g.maxConcurrency)

	for i, cut := range cuts {
		task := keyframeTask{
			index: i,
			cut:   cut,
		}
		eg.Go(func() error {
			resp, err := g.generateCutKeyframe(egCtx, task)
			if err != nil {
				return err
			}
			images[task.index] = resp
			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		return nil, err
	}
	return images, nil
}

func (g *Generator) generateCutKeyframe(ctx context.Context, task keyframeTask) (*imagePorts.ImageResponse, error) {
	if err := g.limiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("wait for keyframe rate limiter: %w", err)
	}

	char := g.characterForCut(task.cut)
	if char == nil {
		return nil, fmt.Errorf("character not found for character ID '%s'", task.cut.CharacterID)
	}

	req := g.buildImageRequest(task.cut, char)
	logger := newKeyframeLogger(task, char, req.Image.FileAPIURI)

	logger.Info("Starting keyframe generation")
	startTime := time.Now()

	resp, err := g.generator.GenerateSingleImage(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("cut %d (character_id: %s) keyframe generation failed: %w", task.index+1, char.ID, err)
	}

	logger.Info("Keyframe generation completed",
		"duration", time.Since(startTime).Round(time.Second),
	)

	return resp, nil
}

func (g *Generator) characterForCut(cut ports.Cut) *characterkit.Character {
	return g.composer.Characters.GetCharacterWithDefault(cut.CharacterID)
}

func (g *Generator) buildImageRequest(cut ports.Cut, char *characterkit.Character) imagePorts.SingleImageRequest {
	userPrompt, systemPrompt := g.pb.BuildCut(cut, char)
	fileURI := g.composer.GetCharacterResourceURI(char.ID)

	return imagePorts.SingleImageRequest{
		GenerationOptions: imagePorts.GenerationOptions{
			Model:          g.model,
			Prompt:         userPrompt,
			SystemPrompt:   systemPrompt,
			NegativePrompt: negativeKeyframePrompt,
			AspectRatio:    CutAspectRatio,
			ImageSize:      ImageSize2K,
			Seed:           char.Seed,
		},
		Image: imagePorts.ImageURI{
			FileAPIURI:   fileURI,
			ReferenceURL: char.ReferenceURL,
		},
	}
}

func newKeyframeLogger(task keyframeTask, char *characterkit.Character, fileURI string) *slog.Logger {
	return slog.With(
		"keyframe_index", task.index+1,
		"character_id", char.ID,
		"character_name", char.Name,
		"seed", char.Seed,
		"use_file_api", fileURI != "",
	)
}
