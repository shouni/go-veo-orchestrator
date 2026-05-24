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

// negativePagePrompt は生成から除外したい要素を定義します。
const negativePagePrompt = "monochrome, black and white, greyscale, screentone, hatching, dot shades, ink sketch, line art only, realistic photos, 3d render, watermark, signature, deformed faces, bad anatomy, disfigured, poorly drawn hands, extra panels, unexpected panels, more than specified panels, split panels"

type PageGenerator struct {
	composer         *MangaComposer
	generator        PageImageGenerator
	pb               ports.ImagePrompt
	model            string
	limiter          *rate.Limiter
	maxConcurrency   int64
	rateInterval     time.Duration
	rateBurst        int
	maxPanelsPerPage int
}

type PageImageGenerator interface {
	GenerateFusedImage(ctx context.Context, req imagePorts.ImageFusionRequest) (*imagePorts.ImageResponse, error)
}

// NewPageGenerator は、PageGeneratorの新しいインスタンスを作成します。
func NewPageGenerator(
	composer *MangaComposer,
	generator PageImageGenerator,
	pb ports.ImagePrompt,
	model string,
	opts ...PageOption,
) *PageGenerator {
	g := &PageGenerator{
		composer:         composer,
		generator:        generator,
		pb:               pb,
		model:            model,
		maxConcurrency:   ports.DefaultMaxConcurrency,
		rateInterval:     defaultRateInterval,
		rateBurst:        defaultRateBurst,
		maxPanelsPerPage: defaultMaxPanelsPerPage,
	}

	for _, opt := range opts {
		opt(g)
	}

	g.limiter = rate.NewLimiter(rate.Every(g.rateInterval), g.rateBurst)

	return g
}

// Execute は、errgroupの制限機能を使用して並列数を制御しながらページ画像を生成します。
func (g *PageGenerator) Execute(ctx context.Context, manga *ports.MangaResponse) ([]*imagePorts.ImageResponse, error) {
	if manga == nil || len(manga.Panels) == 0 {
		return nil, nil
	}

	if err := g.composer.PrepareCharacterResources(ctx, manga.Panels); err != nil {
		return nil, fmt.Errorf("failed to prepare character resources: %w", err)
	}
	if err := g.composer.PreparePanelResources(ctx, manga.Panels); err != nil {
		return nil, fmt.Errorf("failed to prepare panel resources: %w", err)
	}

	maxPanels := g.maxPanelsPerPage
	panelGroups := chunkPanels(manga.Panels, maxPanels)
	totalPages := len(panelGroups)
	allResponses := make([]*imagePorts.ImageResponse, totalPages)

	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(int(g.maxConcurrency))

	for i, group := range panelGroups {
		seed := g.determineDefaultSeed(group)
		currentPageNum := i + 1

		eg.Go(func() error {
			if err := g.limiter.Wait(egCtx); err != nil {
				return err
			}

			subManga := ports.MangaResponse{
				Title:       fmt.Sprintf("%s (Page %d/%d)", manga.Title, currentPageNum, totalPages),
				Description: manga.Description,
				Panels:      group,
			}

			logger := slog.With(
				"page", currentPageNum,
				"total", totalPages,
				"panels", len(group),
				"seed", seed,
			)
			logger.Info("Starting manga page generation")

			startTime := time.Now()
			res, err := g.generateMangaPage(egCtx, subManga, seed)
			if err != nil {
				return fmt.Errorf("failed to generate page %d: %w", currentPageNum, err)
			}

			logger.Info("Manga page generation completed", "duration", time.Since(startTime).Round(time.Second))
			allResponses[i] = res
			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		return nil, err
	}
	return allResponses, nil
}

// generateMangaPage は、提供されたマンガレスポンスとAIベースの画像生成用のシードを使用して、マンガページの画像を生成します。
func (g *PageGenerator) generateMangaPage(ctx context.Context, manga ports.MangaResponse, seed int64) (*imagePorts.ImageResponse, error) {
	// 1. リソース収集とインデックスマッピングの作成
	resMap := g.collectResources(manga.Panels)

	// 2. プロンプト構築
	userPrompt, systemPrompt := g.pb.BuildPage(manga.Panels, resMap)

	// 3. ImageURI 構造体のスライスを作成
	req := imagePorts.ImageFusionRequest{
		GenerationOptions: imagePorts.GenerationOptions{
			Model:          g.model,
			Prompt:         userPrompt,
			SystemPrompt:   systemPrompt,
			NegativePrompt: negativePagePrompt,
			AspectRatio:    PageAspectRatio,
			ImageSize:      ImageSize2K,
			Seed:           &seed,
		},
		Images: resMap.OrderedAssets,
	}

	slog.Info("Requesting AI image generation",
		"title", manga.Title,
		"seed", seed,
		"total_assets", len(resMap.OrderedAssets),
	)

	return g.generator.GenerateFusedImage(ctx, req)
}

// collectResources は、ページ内のキャラクター立ち絵とパネル参照画像を整理し、インデックスを割り振ります。
func (g *PageGenerator) collectResources(panels []ports.Panel) *ports.ResourceMap {
	g.composer.mu.RLock()
	defer g.composer.mu.RUnlock()

	collector := newPageResourceCollector(g.composer)
	collector.addCharacterAssets(panels)
	collector.addPanelAssets(panels)
	return collector.resourceMap
}

// determineDefaultSeed はキャラクターデータを基にページ生成時のデフォルトシード値を決定します。
func (g *PageGenerator) determineDefaultSeed(panels []ports.Panel) int64 {
	const defaultSeed = 1000
	if len(panels) == 0 {
		return defaultSeed
	}
	cm := g.composer.CharactersMap

	// 最初のパネルの話者 Seed を優先します。
	if char := cm.GetCharacter(panels[0].SpeakerID); char != nil && char.Seed > 0 {
		return char.Seed
	}

	// 次にデフォルトキャラクターの Seed を参照します。
	if defaultChar := cm.GetDefault(); defaultChar != nil && defaultChar.Seed > 0 {
		return defaultChar.Seed
	}

	return defaultSeed
}

// chunkPanels はパネルのスライスを指定サイズのチャンクに分割して返します。
func chunkPanels(panels []ports.Panel, size int) [][]ports.Panel {
	var chunks [][]ports.Panel
	for i := 0; i < len(panels); i += size {
		end := i + size
		if end > len(panels) {
			end = len(panels)
		}
		chunks = append(chunks, panels[i:end])
	}
	return chunks
}
