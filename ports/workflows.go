package ports

import (
	"context"
	"sync"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
)

// Workflows は、構築済みの各 Runner を保持します。
type Workflows struct {
	Script      ScriptRunner
	CutKeyframe CutKeyframeRunner
	Video       VideoTimelineRunner
	Publish     VideoPublishRunner

	closeOnce sync.Once
	onClose   func()
}

// SetCloseFunc は、Close で一度だけ実行するクリーンアップ処理を登録します。
// 画像キャッシュのバックグラウンド goroutine など、workflow.New が確保した
// リソースの解放を接続するために構築側（workflow パッケージ）が使う想定です。
// ゼロ値や手組みの Workflows には登録がなく、Close は何もしません。
func (w *Workflows) SetCloseFunc(fn func()) {
	w.onClose = fn
}

// Close は、workflow.New で構築した Workflows が確保したバックグラウンド
// リソース（現状は画像キャッシュの定期クリーンアップ goroutine）を解放します。
// 複数回呼んでも安全で、2回目以降は何もしません。
func (w *Workflows) Close() error {
	w.closeOnce.Do(func() {
		if w.onClose != nil {
			w.onClose()
		}
	})
	return nil
}

// ScriptRunner は、ソース（URLやテキスト）を解析し、Music Recipe を含む動画台本を生成する責務を持ちます。
type ScriptRunner interface {
	Run(ctx context.Context, scriptURL string, mode string) (*VideoRecipe, error)
}

// CutKeyframeRunner は、解析済みの動画データを基に、カットのキーフレーム画像を生成する責務を持ちます。
type CutKeyframeRunner interface {
	Run(ctx context.Context, recipe *VideoRecipe) ([]*imagePorts.ImageResponse, error)
	RunAndSave(ctx context.Context, recipe *VideoRecipe, outputPath string) (*VideoRecipe, error)
	// EditAndSave edits the existing keyframe image of a single-cut recipe using editPrompt
	// (preserving composition/pose rather than regenerating from scratch), saves the result
	// the same way RunAndSave does, and returns the recipe with the updated KeyframeReference.
	// recipe must contain exactly one cut, and that cut's KeyframeReference must already point
	// at the image to edit. Returns an error if the configured image generator does not support
	// editing.
	EditAndSave(ctx context.Context, recipe *VideoRecipe, editPrompt string, outputPath string) (*VideoRecipe, error)
}

// VideoPublishRunner は、動画レシピと生成済みカットのメタデータを JSON として出力する責務を持ちます。
type VideoPublishRunner interface {
	Run(ctx context.Context, recipe *VideoRecipe, outputDir string) (*PublishResult, error)
	BuildMetadata(recipe *VideoRecipe) ([]byte, error)
}
