package ports

import (
	"context"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	characterkit "github.com/shouni/go-character-kit/character"
)

// TemplateData はスクリプト生成プロンプトのテンプレートに渡す構造化入力です。
type TemplateData struct {
	SourceRecipe *VideoRecipe
}

// ScriptPrompt は、AIプロンプトを構築する契約です。
type ScriptPrompt interface {
	Build(mode string, data *TemplateData) (string, error)
}

// KeyframePrompt は、カットのキーフレーム画像生成AI向けのプロンプトを構築する契約です。
type KeyframePrompt interface {
	BuildCut(cut Cut, char *characterkit.Character) (userPrompt string, systemPrompt string)
	// BuildEdit builds the user/system prompt for editing an existing keyframe image with
	// editPrompt, reinforcing character identity and style consistency (art style, negative
	// prompt guidance) the same way BuildCut does for full generation.
	BuildEdit(cut Cut, char *characterkit.Character, editPrompt string) (userPrompt string, systemPrompt string)
}

// CutImageGenerator は、一連のカットのキーフレーム画像レスポンスを生成します。
type CutImageGenerator interface {
	Execute(ctx context.Context, cuts []Cut) ([]*imagePorts.ImageResponse, error)
}
