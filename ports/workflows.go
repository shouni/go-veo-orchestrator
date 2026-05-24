package ports

import (
	"context"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
)

// Workflows は、構築済みの各 Runner を保持します。
type Workflows struct {
	Design     DesignRunner
	Script     ScriptRunner
	PanelImage PanelImageRunner
	PageImage  PageImageRunner
	Publish    PublishRunner
}

// DesignRunner は、キャラクターIDに基づいてデザインシートを生成し、Seed値を特定する責務を持ちます。
type DesignRunner interface {
	Run(ctx context.Context, charIDs []string, seed int64, outputDir string) (string, int64, error)
}

// ScriptRunner は、ソース（URLやテキスト）を解析し、構造化された漫画台本を生成する責務を持ちます。
type ScriptRunner interface {
	Run(ctx context.Context, scriptURL string, mode string) (*MangaResponse, error)
}

// PanelImageRunner は、解析済みの漫画データと対象パネルのインデックスを基に、パネル画像を生成する責務を持ちます。
type PanelImageRunner interface {
	Run(ctx context.Context, manga *MangaResponse) ([]*imagePorts.ImageResponse, error)
	RunAndSave(ctx context.Context, manga *MangaResponse, outputPath string) (*MangaResponse, error)
}

// PageImageRunner は、解析済みの漫画データから漫画のページ画像を生成する責務を持ちます。
type PageImageRunner interface {
	Run(ctx context.Context, manga *MangaResponse) ([]*imagePorts.ImageResponse, error)
	RunAndSave(ctx context.Context, manga *MangaResponse, outputPath string) ([]string, error)
}

// PublishRunner は、漫画データを統合し、指定された形式（例: HTML）で出力する責務を持ちます。
type PublishRunner interface {
	Run(ctx context.Context, manga *MangaResponse, outputDir string) (*PublishResult, error)
	BuildMarkdown(manga *MangaResponse) string
}
