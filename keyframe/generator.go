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
	composer       CharacterResourceProvider
	generator      ImageGenerator
	pb             ports.KeyframePrompt
	model          string
	limiter        *rate.Limiter
	maxConcurrency int
	rateInterval   time.Duration
	rateBurst      int
	aspectRatio    string
}

// ImageGenerator は単一画像生成を実行する依存インターフェースです。
type ImageGenerator interface {
	GenerateSingleImage(ctx context.Context, req imagePorts.SingleImageRequest) (*imagePorts.ImageResponse, error)
}

// CharacterResourceProvider は、Generator が Composer に依存している範囲だけを切り出した
// 契約です。キャラクター参照画像の事前準備・カットからのキャラクター解決・準備済み画像 URI の
// 参照を提供します。*Composer がこれを実装しますが、Generator を単体テストする際は
// AssetManager/Backend を伴う本物の Composer を組み立てずに、この interface を満たす軽量な
// fake を渡せます。
type CharacterResourceProvider interface {
	PrepareCharacterResources(ctx context.Context, cuts []ports.Cut) error
	CharacterForCut(cut ports.Cut) *characterkit.Character
	GetResourceURI(referenceURL string) string
}

type keyframeTask struct {
	index int
	cut   ports.Cut
}

// NewGenerator は Generator の新しいインスタンスを初期化します。
func NewGenerator(
	composer CharacterResourceProvider,
	generator ImageGenerator,
	pb ports.KeyframePrompt,
	model string,
	opts ...Option,
) *Generator {
	g := &Generator{
		composer:  composer,
		generator: generator,
		pb:        pb,
		model:     model,
	}

	applyDefaultOptions(g)
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

// EditCut edits an existing keyframe image for a single cut using a text instruction
// (cut.KeyframeReference as the source image), preserving composition/pose/background and
// changing only what editPrompt specifies. It reuses the same conversational image model as
// Execute/GenerateSingleImage (Gemini's multimodal "Nano Banana" image models support editing
// an input image via a plain generateContent call), rather than a dedicated edit API — Vertex
// AI's Imagen mask-based edit/capability models have no supported successor.
func (g *Generator) EditCut(ctx context.Context, cut ports.Cut, editPrompt string) (*imagePorts.ImageResponse, error) {
	if err := g.limiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("wait for keyframe rate limiter: %w", err)
	}
	if cut.KeyframeReference == "" {
		return nil, fmt.Errorf("cut %d has no existing keyframe to edit", cut.CutIndex)
	}

	char := g.characterForCut(cut)
	if char == nil {
		return nil, fmt.Errorf("cut %d: character not found for character ID '%s'", cut.CutIndex, cut.CharacterID)
	}

	userPrompt, systemPrompt := g.pb.BuildEdit(cut, char, editPrompt)
	req := imagePorts.SingleImageRequest{
		GenerationOptions: imagePorts.GenerationOptions{
			Model:          g.model,
			Prompt:         userPrompt,
			SystemPrompt:   systemPrompt,
			NegativePrompt: negativeKeyframePrompt,
			AspectRatio:    g.aspectRatio,
			ImageSize:      ImageSize2K,
			Seed:           char.Seed,
		},
		Image: imagePorts.ImageURI{ReferenceURL: cut.KeyframeReference},
	}

	logger := slog.With(
		"cut_index", cut.CutIndex,
		"character_id", char.ID,
		"character_name", char.Name,
	)
	logger.Info("Starting keyframe edit")
	startTime := time.Now()

	resp, err := g.generator.GenerateSingleImage(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("cut %d (character_id: %s) keyframe edit failed: %w", cut.CutIndex, char.ID, err)
	}

	logger.Info("Keyframe edit completed",
		"duration", time.Since(startTime).Round(time.Second),
	)

	return resp, nil
}

func (g *Generator) characterForCut(cut ports.Cut) *characterkit.Character {
	return g.composer.CharacterForCut(cut)
}

func (g *Generator) buildImageRequest(cut ports.Cut, char *characterkit.Character) imagePorts.SingleImageRequest {
	userPrompt, systemPrompt := g.pb.BuildCut(cut, char)
	// キャラクターの参照画像が生成対象と異なるアスペクト比（例: 横長3ポーズシートを縦長
	// キーフレームの参照に使う）だと、色・小物配置・髪型などの細部が生成のたびにブレやすいため、
	// g.aspectRatio に一致する参照画像（ReferenceURLs）があればそちらを優先します。
	referenceURL := char.ReferenceURLFor(g.aspectRatio)
	fileURI := g.composer.GetResourceURI(referenceURL)

	return imagePorts.SingleImageRequest{
		GenerationOptions: imagePorts.GenerationOptions{
			Model:          g.model,
			Prompt:         userPrompt,
			SystemPrompt:   systemPrompt,
			NegativePrompt: negativeKeyframePrompt,
			AspectRatio:    g.aspectRatio,
			ImageSize:      ImageSize2K,
			Seed:           char.Seed,
		},
		Image: imagePorts.ImageURI{
			FileAPIURI:   fileURI,
			ReferenceURL: referenceURL,
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
