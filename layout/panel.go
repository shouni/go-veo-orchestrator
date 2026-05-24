package layout

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"

	"github.com/shouni/go-veo-orchestrator/ports"
)

// negativePanelPrompt は単体カットのキーフレームで「文字」や「フキダシ」を排除するための指定です。
const negativePanelPrompt = "speech bubble, dialogue balloon, text, alphabet, letters, words, signatures, watermark, username, low quality, distorted, bad anatomy, monochrome, black and white, greyscale"

// PanelGenerator は、キャラクターの一貫性を保ちながら並列で複数カットのキーフレームを生成します。
type PanelGenerator struct {
	composer       *MangaComposer
	generator      PanelImageGenerator
	pb             ports.ImagePrompt
	model          string
	limiter        *rate.Limiter
	maxConcurrency int
	rateInterval   time.Duration
	rateBurst      int
}

type PanelImageGenerator interface {
	GenerateSingleImage(ctx context.Context, req imagePorts.SingleImageRequest) (*imagePorts.ImageResponse, error)
}

// NewPanelGenerator は PanelGenerator の新しいインスタンスを初期化します。
func NewPanelGenerator(
	composer *MangaComposer,
	generator PanelImageGenerator,
	pb ports.ImagePrompt,
	model string,
	opts ...PanelOption,
) *PanelGenerator {
	g := &PanelGenerator{
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
func (g *PanelGenerator) Execute(ctx context.Context, panels []ports.Panel) ([]*imagePorts.ImageResponse, error) {
	if len(panels) == 0 {
		return nil, nil
	}

	if err := g.composer.PrepareCharacterResources(ctx, panels); err != nil {
		return nil, err
	}

	images := make([]*imagePorts.ImageResponse, len(panels))
	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(g.maxConcurrency)

	cm := g.composer.CharactersMap

	for i, panel := range panels {
		eg.Go(func() error {
			if err := g.limiter.Wait(egCtx); err != nil {
				return err
			}

			charID := panel.CharacterID
			if charID == "" {
				charID = panel.SpeakerID
			}
			char := cm.GetCharacterWithDefault(charID)
			if char == nil {
				return fmt.Errorf("character not found for character ID '%s'", charID)
			}
			seed := char.Seed
			userPrompt, systemPrompt := g.pb.BuildPanel(panel, char)
			fileURI := g.composer.GetCharacterResourceURI(char.ID)

			logger := slog.With(
				"panel_index", i+1,
				"character_id", char.ID,
				"character_name", char.Name,
				"seed", seed,
				"use_file_api", fileURI != "",
			)
			logger.Info("Starting panel generation")

			startTime := time.Now()
			resp, err := g.generator.GenerateSingleImage(egCtx, imagePorts.SingleImageRequest{
				GenerationOptions: imagePorts.GenerationOptions{
					Model:          g.model,
					Prompt:         userPrompt,
					SystemPrompt:   systemPrompt,
					NegativePrompt: negativePanelPrompt,
					AspectRatio:    PanelAspectRatio,
					ImageSize:      ImageSize1K,
					Seed:           &seed,
				},
				Image: imagePorts.ImageURI{
					FileAPIURI:   fileURI,
					ReferenceURL: char.ReferenceURL,
				},
			})
			if err != nil {
				return fmt.Errorf("cut %d (character_id: %s) keyframe generation failed: %w", i+1, char.ID, err)
			}

			logger.Info("Panel generation completed",
				"duration", time.Since(startTime).Round(time.Second),
			)
			images[i] = resp
			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		return nil, err
	}
	return images, nil
}
