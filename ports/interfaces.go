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
// 旧実装互換のためメソッド名は BuildPanel / BuildPage を維持しています。
type ImagePrompt interface {
	// BuildPanel は、単一カットのキーフレーム用ユーザープロンプトとシステムプロンプトを決定します。
	BuildPanel(panel Panel, char *Character) (userPrompt string, systemPrompt string)
	// BuildPage は、複数カットを統合したシーン画像用のユーザープロンプトとシステムプロンプトを生成します。
	BuildPage(panels []Panel, rm *ResourceMap) (userPrompt string, systemPrompt string)
}

// VideoImagePrompt は動画用語で定義した新しいプロンプト契約です。
type VideoImagePrompt interface {
	BuildCut(cut Cut, char *Character) (userPrompt string, systemPrompt string)
	BuildScene(cuts []Cut, rm *ResourceMap) (userPrompt string, systemPrompt string)
}

// CutImageGenerator は、指定されたコンテキスト内で一連のカットの画像レスポンスを生成するためのインターフェースを定義します。
type CutImageGenerator interface {
	Execute(ctx context.Context, cuts []Cut) ([]*imagePorts.ImageResponse, error)
}

// SceneImageGenerator は、与えられた動画レシピに基づいてシーン画像データを生成します。
type SceneImageGenerator interface {
	Execute(ctx context.Context, recipe *VideoRecipe) ([]*imagePorts.ImageResponse, error)
}

// PanelsImageGenerator は旧 API 互換のエイリアスです。
type PanelsImageGenerator = CutImageGenerator

// PagesImageGenerator は旧 API 互換のエイリアスです。
type PagesImageGenerator = SceneImageGenerator
