package ports

import "errors"

// パッケージ横断で判定可能な sentinel error 群です。呼び出し側は errors.Is で
// 個別の失敗理由を判定し、汎用エラーとは異なる制御（フォールバックやリトライ）を
// 行えます。

var (
	// ErrRecipeRequired は、VideoRecipe を必須とする処理に nil が渡された場合に
	// 返されます。
	ErrRecipeRequired = errors.New("video recipe is required")

	// ErrEditingNotSupported は、CutKeyframeRunner.EditAndSave の呼び出し時に
	// 設定済みの画像生成エンジンがキーフレーム編集（EditCut）を実装していない場合に
	// 返されます。呼び出し側はこれを検知して、全体再生成へのフォールバックなど
	// 編集不可時の代替フローを選択できます。
	ErrEditingNotSupported = errors.New("configured image generator does not support keyframe editing")

	// ErrInvalidAIResponse は、AI の応答テキストを VideoRecipe の JSON として
	// 解析できなかった場合に返されます。呼び出し側はこれを検知して、
	// プロンプトやネットワークの失敗とは区別したリトライ判断ができます。
	ErrInvalidAIResponse = errors.New("AI response could not be parsed as video recipe JSON")

	// ErrVideoRunnerNotConfigured は、VideoRunner（Veo アダプター）が設定されていない
	// ワークフローで Workflows.Video を呼び出した場合に返されます。
	ErrVideoRunnerNotConfigured = errors.New("video runner is not configured")

	// ErrInputTooLarge は、入力ソースが許容される最大サイズを超えていた場合に
	// 返されます。呼び出し側はこれを検知して、解析失敗とは区別した入力側の
	// 是正（分割やソースの見直し）を促せます。
	ErrInputTooLarge = errors.New("input source exceeds maximum allowed size")

	// ErrNoKeyframeToEdit は、編集対象のカットにまだ既存のキーフレーム画像
	// （KeyframeReference）が設定されていない場合に返されます。呼び出し側は
	// これを検知して、先に GenerateAndSave 等でキーフレームを生成するよう促せます。
	ErrNoKeyframeToEdit = errors.New("cut has no existing keyframe to edit")

	// ErrUnsupportedCutDuration は、カットの尺が、そのカットが解決する Veo の生成モードで
	// 受け付けられない値だった場合に返されます（DurationsForMode 参照）。呼び出し側は
	// これを検知して、Veo API やネットワークの失敗ではなくレシピ側の尺の計画ミスとして
	// 扱えます（リトライしても直りません）。
	ErrUnsupportedCutDuration = errors.New("cut duration is not supported by the resolved Veo generation mode")

	// ErrVideoRunnerRequired は、VideoTimelineRunner に VideoRunner（Veo アダプター）が
	// 注入されていない場合に返されます。ErrVideoRunnerNotConfigured が「未設定の
	// ワークフローを呼んだ」ことを表すのに対し、こちらは構築の誤りを表します。
	ErrVideoRunnerRequired = errors.New("video runner is required")

	// ErrWriterRequired は、成果物の保存先 Writer が注入されていない場合に返されます。
	ErrWriterRequired = errors.New("writer is required")

	// ErrConfigInvalid は、Config.Validate が必須項目の欠落を検出したことを表します
	// （モデル名 2 種と、画作りの KeyframeAspectRatio / KeyframeImageSize。理由は
	// Config のコメント参照）。実行時の失敗ではなく組み立ての誤りなので、
	// workflow.New の時点で返ります。
	ErrConfigInvalid = errors.New("config is invalid")
)
