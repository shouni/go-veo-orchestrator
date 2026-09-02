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
	// BuildEdit は、既存のキーフレーム画像を editPrompt で編集するためのプロンプトを
	// 構築します。キャラクターの同一性と画風の一貫性（アートスタイル、ネガティブプロンプト）は
	// BuildCut と同じように補強します。
	BuildEdit(cut video.Cut, char *characterkit.Character, editPrompt string) (userPrompt string, systemPrompt string)
}

// CutImageGenerator は、カット 1 件のキーフレーム画像を生成します。
//
// 1 枚単位なのは意図的です。呼び出し側（CutKeyframeRunner）が 1 枚できるたびに
// 即座に保存できるようにするためで、まとめて生成する形が何を失うかは
// CutKeyframeRunner.GenerateAndSave のコメントを参照してください。並列実行と順序の保持、
// および index / total（進捗ログ用の「何枚中の何枚目か」）の採番は呼び出し側の仕事です。
type CutImageGenerator interface {
	GenerateCut(ctx context.Context, cut video.Cut, index, total int) (*video.KeyframeImage, error)
}
