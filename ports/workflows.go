package ports

import (
	"context"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
)

// Workflows は、構築済みの各 Runner を保持します。
type Workflows struct {
	Design      DesignRunner
	Script      ScriptRunner
	CutKeyframe CutKeyframeRunner
	Video       VideoTimelineRunner
	Publish     VideoPublishRunner

	// 旧 API 互換フィールド。
	PanelImage PanelImageRunner
}

// DesignRunner は、キャラクターIDに基づいてデザインシートを生成し、Seed値を特定する責務を持ちます。
type DesignRunner interface {
	Run(ctx context.Context, charIDs []string, seed int64, outputDir string) (string, int64, error)
}

// ScriptRunner は、ソース（URLやテキスト）を解析し、Music Recipe を含む動画台本を生成する責務を持ちます。
type ScriptRunner interface {
	Run(ctx context.Context, scriptURL string, mode string) (*MangaResponse, error)
}

// CutKeyframeRunner は、解析済みの動画データを基に、カットのキーフレーム画像を生成する責務を持ちます。
type CutKeyframeRunner interface {
	Run(ctx context.Context, manga *MangaResponse) ([]*imagePorts.ImageResponse, error)
	RunAndSave(ctx context.Context, manga *MangaResponse, outputPath string) (*MangaResponse, error)
}

// VideoPublishRunner は、動画レシピと生成済みカットのメタデータを JSON として出力する責務を持ちます。
type VideoPublishRunner interface {
	Run(ctx context.Context, recipe *VideoRecipe, outputDir string) (*PublishResult, error)
	BuildMetadata(recipe *VideoRecipe) ([]byte, error)
}

// PanelImageRunner は旧 API 互換のエイリアスです。
type PanelImageRunner = CutKeyframeRunner

// PublishRunner は旧 API 互換のエイリアスです。
type PublishRunner = VideoPublishRunner
