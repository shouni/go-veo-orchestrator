# ⚠️ Sentinel Errors

[← README](../README.md)

呼び出し側が `errors.Is` で判定し、汎用エラーとは異なる制御（フォールバックやリトライ）を行えるよう、`ports` パッケージは以下の sentinel error を公開しています。

| エラー | 発生条件 | 想定される呼び出し側の対応 |
| --- | --- | --- |
| `ports.ErrConfigInvalid` | `Config.Validate` が必須項目（`GeminiModel` / `ImageModel`）の欠落を検出した場合。`workflow.New` の時点で返ります | 構築（DI）の誤り。設定不備の通知 |
| `ports.ErrRecipeRequired` | `VideoRecipe` が必須の処理に `nil` を渡した場合 | 呼び出し側の実装不備。基本的に発生させない |
| `ports.ErrEditingNotSupported` | `EditAndSave` で、設定済みの画像生成エンジンがキーフレーム編集（`EditCut`）を実装していない場合 | 全体再生成（`GenerateAndSave`）へのフォールバック |
| `ports.ErrInvalidAIResponse` | AI の応答テキストを `VideoRecipe` の JSON として解析できなかった場合 | ネットワーク/認証エラーと区別したリトライ判断 |
| `ports.ErrVideoRunnerNotConfigured` | `VideoRunner` 未設定のまま `Workflows.Video` を呼び出した場合 | 動画生成ステップのスキップ、設定不備の通知 |
| `ports.ErrInputTooLarge` | ソースの入力サイズが許容上限（5MB）を超えた場合 | 入力の分割やソースの見直しを促す |
| `ports.ErrUnsupportedCutDuration` | カットの尺が、解決した Veo 生成モードで受け付けられない値だった場合 | レシピ側の尺の計画ミス。リトライせず `veo.SnapDuration` 等で尺を割り当て直す |
| `ports.ErrNoKeyframeToEdit` | `EditAndSave` の対象カットに既存のキーフレームが無い場合 | 先に `GenerateAndSave` でキーフレームを生成させる |
| `video.ErrRecipeInvalid` | `VideoRecipe.Validate` が構造上の問題（タイトル欠落・カット無し・範囲外の `section_index`）を検出した場合 | AI 生成なら再生成、手入力なら入力の是正 |
| `ports.ErrVideoRunnerRequired` / `ErrWriterRequired` | Runner や Writer が注入されないまま実行された場合 | 構築（DI）の誤り。設定不備の通知 |
