package ports

import (
	"context"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
)

// TemplateData はスクリプト生成プロンプトのテンプレートに渡すデータ構造です。
type TemplateData struct {
	InputText string
}

// ScriptPrompt は、AIプロンプトを構築する契約です。
type ScriptPrompt interface {
	// Build は、指定されたモードとデータ（TemplateData）に基づいてプロンプトを生成します。
	// 注意: data に nil を指定することはできません。
	Build(mode string, data *TemplateData) (string, error)
}

// ImagePrompt は、画像生成AI向けのプロンプトを構築する契約です。
type ImagePrompt interface {
	// BuildPanel は、単一の漫画パネル用のユーザープロンプトとシステムプロンプトを決定します。
	BuildPanel(panel Panel, char *Character) (userPrompt string, systemPrompt string)
	// BuildPage は、統合された漫画ページ画像用のユーザープロンプトと システムプロンプトを生成します。
	BuildPage(panels []Panel, rm *ResourceMap) (userPrompt string, systemPrompt string)
}

// PanelsImageGenerator は、指定されたコンテキスト内で一連のパネルの画像レスポンスを生成するためのインターフェースを定義します。
type PanelsImageGenerator interface {
	Execute(ctx context.Context, panels []Panel) ([]*imagePorts.ImageResponse, error)
}

// PagesImageGenerator は、与えられた漫画レスポンスに基づいて漫画ページの画像データを生成します。
// パネルを処理し、画像レスポンスのスライスまたは失敗時にエラーを出力します。
type PagesImageGenerator interface {
	Execute(ctx context.Context, manga *MangaResponse) ([]*imagePorts.ImageResponse, error)
}
