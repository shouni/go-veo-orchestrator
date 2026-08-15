package ports

import (
	"context"
)

// Workflows は、構築済みの各 Runner を保持します。
//
// Close はありません。参照画像を gs:// URI のまま渡すようになり、画像キャッシュと
// その定期クリーンアップ goroutine が無くなったためです（発射間隔のリミッターと
// singleflight は解放すべき資源を持ちません）。呼び出し側の defer は不要です。
type Workflows struct {
	Script      ScriptRunner
	CutKeyframe CutKeyframeRunner
	Video       VideoTimelineRunner
	Publish     VideoPublishRunner
}

// ScriptRunner は、ソース（URLやテキスト）を解析し、Music Recipe を含む動画台本を生成する責務を持ちます。
type ScriptRunner interface {
	Run(ctx context.Context, scriptURL string, mode string) (*VideoRecipe, error)
}

// CutKeyframeRunner は、解析済みの動画データを基に、カットのキーフレーム画像を生成する責務を持ちます。
type CutKeyframeRunner interface {
	// Run generates keyframe images only for the cuts that have no KeyframeReference yet and
	// returns a slice aligned with recipe.Cuts — nil at the positions that already had one.
	// Callers treat nil as "reuse the cut's existing reference".
	Run(ctx context.Context, recipe *VideoRecipe) ([]*KeyframeImage, error)
	RunAndSave(ctx context.Context, recipe *VideoRecipe, outputPath string) (*VideoRecipe, error)
	// EditAndSave edits the existing keyframe image of recipe.Cuts[cutPosition] using
	// editPrompt (preserving composition/pose rather than regenerating from scratch), saves
	// the result the same way RunAndSave does, and returns the recipe with the updated
	// KeyframeReference. The target cut's KeyframeReference must already point at the image
	// to edit. Returns an error if the configured image generator does not support editing.
	EditAndSave(ctx context.Context, recipe *VideoRecipe, cutPosition int, editPrompt string, outputPath string) (*VideoRecipe, error)
}

// VideoPublishRunner は、動画レシピと生成済みカットのメタデータを JSON として出力する責務を持ちます。
type VideoPublishRunner interface {
	Run(ctx context.Context, recipe *VideoRecipe, outputDir string) (*PublishResult, error)
	BuildMetadata(recipe *VideoRecipe) ([]byte, error)
}
