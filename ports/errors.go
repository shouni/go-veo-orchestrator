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
)
