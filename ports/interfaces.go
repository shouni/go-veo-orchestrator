package ports

import (
	"context"

	characterkit "github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-veo-orchestrator/video"
)

// TemplateData はスクリプト生成プロンプトのテンプレートに渡す構造化入力です。
type TemplateData struct {
	SourceRecipe *video.Recipe
}

// ScriptPrompt は、AIプロンプトを構築する契約です。
type ScriptPrompt interface {
	Build(mode string, data *TemplateData) (string, error)
}

// KeyframePrompt は、カットのキーフレーム画像生成AI向けのプロンプトを構築する契約です。
type KeyframePrompt interface {
	BuildCut(cut video.Cut, char *characterkit.Character) (userPrompt string, systemPrompt string)
	// BuildEdit builds the user/system prompt for editing an existing keyframe image with
	// editPrompt, reinforcing character identity and style consistency (art style, negative
	// prompt guidance) the same way BuildCut does for full generation.
	BuildEdit(cut video.Cut, char *characterkit.Character, editPrompt string) (userPrompt string, systemPrompt string)
}

// CutImageGenerator は、カット 1 件のキーフレーム画像を生成します。
//
// **1 枚単位なのは意図的です。** 呼び出し側（CutKeyframeRunner）は 1 枚できるたびに
// 即座に保存し、KeyframeReference を更新します。まとめて生成してから保存すると、
// 途中でプロセスが落ちた場合に生成済み（＝課金済み）の画像がメモリごと失われ、
// 再実行で全部作り直しになります。並列実行と順序の保持は呼び出し側の仕事です。
//
// index / total は進捗ログ用です（「何枚中の何枚目か」）。1 枚に分単位掛かるため、
// これが無いと実行中に進捗を判断できません。
type CutImageGenerator interface {
	GenerateCut(ctx context.Context, cut video.Cut, index, total int) (*video.KeyframeImage, error)
}
