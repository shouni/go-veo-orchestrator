package ports

import (
	"context"

	"github.com/shouni/go-veo-orchestrator/video"
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
	Run(ctx context.Context, scriptURL string, mode string) (*video.Recipe, error)
}

// CutKeyframeRunner は、Workflows.CutKeyframe が利用側へ公開する操作です。
//
// 生成だけを行う Generate は含めません。成果物を保存せずに画像だけ受け取っても
// recipe の KeyframeReference が更新されず、利用側は自分でファイル名規約を
// 再実装する羽目になります（保存を含む2つが実際の入口です）。
type CutKeyframeRunner interface {
	// GenerateAndSave は未生成のカットのキーフレームを生成して保存し、
	// KeyframeReference を更新した recipe を返します。
	GenerateAndSave(ctx context.Context, recipe *video.Recipe, outputPath string) (*video.Recipe, error)
	// EditAndSave は video.Cuts[cutPosition] の既存キーフレームを editPrompt で編集し
	// （一から作り直すのではなく構図・ポーズを保ちます）、GenerateAndSave と同じ方法で
	// 保存して、KeyframeReference を更新した recipe を返します。対象カットの
	// KeyframeReference は編集元の画像を指している必要があります。
	// 注入された画像ジェネレータが編集に対応していない場合はエラーを返します。
	EditAndSave(ctx context.Context, recipe *video.Recipe, cutPosition int, editPrompt string, outputPath string) (*video.Recipe, error)
}

// VideoPublishRunner は、動画レシピと生成済みカットのメタデータを JSON として出力する責務を持ちます。
type VideoPublishRunner interface {
	Run(ctx context.Context, recipe *video.Recipe, outputDir string) (*video.PublishResult, error)
	BuildMetadata(recipe *video.Recipe) ([]byte, error)
}
