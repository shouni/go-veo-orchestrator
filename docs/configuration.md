# ⚙️ 設定と差し替え (Config / DI)

[← README](../README.md)

`workflow.New(ManagerArgs)` に渡す設定と依存です。

## ports.Config

**モデル名2種は必須**です（未設定なら `ports.ErrConfigInvalid`）。モデル ID は Google 側の都合で世代交代する外部の識別子で、ライブラリが既定値を持つとアプリを更新してもライブラリを更新するまで古いモデルが残るためです。それ以外はゼロ値で構いません（`ApplyDefaults` が補完します）。

| フィールド | 役割 |
| --- | --- |
| `GeminiModel` | 台本生成に使うテキストモデル（**必須**） |
| `ImageModel` | キーフレーム画像生成に使うモデル（**必須**） |
| `MaxConcurrency` | キーフレーム生成の最大並列数（動画生成は Video-to-Video 連鎖のため常に逐次です） |
| `RateInterval` | キーフレーム生成の発射間隔の下限 |
| `RequestTimeout` | AI 呼び出し1回あたりの上限時間（既定 5 分）。レート制限の待機はこの外側なので、混雑がタイムアウトに化けません |
| `KeyframeAspectRatio` | キーフレームのアスペクト比。空なら `keyframe.CutAspectRatio` |

`Config.WithModels(gemini, image)` / `Config.WithAspectRatio(ratio)` で部分的に上書きしたコピーを取得できます（空文字は「変更なし」）。

## ManagerArgs

| フィールド | 役割 |
| --- | --- |
| `AIClient` | `gemini.Model`（台本生成・キーフレーム生成） |
| `Reader` / `Writer` | `ports.ContentReader` / `remoteio.Writer`（レシピの読み込み・成果物の保存） |
| `HTTPClient` | 参照画像の取得に使う HTTP クライアント |
| `VideoRunner` | Veo API アダプタ。未設定なら `ErrVideoRunnerNotConfigured` を返すダミーになります |
| `PromptDeps` | キャラクター定義とプロンプト実装（下記） |

## PromptDeps

プロンプトはこのライブラリに含めず、`PromptDeps` から注入します。

| フィールド | インターフェース | 役割 |
| --- | --- | --- |
| `Characters` | `*characterkit.Characters` | キャラクター定義。カットの `CharacterID` から解決し、未知IDは既定キャラクターへ落とします |
| `ScriptPrompt` | `ports.ScriptPrompt` | Music Recipe から動画台本を生成するプロンプト（`Build(mode, *TemplateData)`） |
| `KeyframePrompt` | `ports.KeyframePrompt` | カットのキーフレーム生成・編集のプロンプト（`BuildCut` / `BuildEdit`） |

## キーフレーム生成のオプション

細部は `keyframe.Option` で調整します（`workflow` が `Config` から組み立てます）。

| オプション | 役割 |
| --- | --- |
| `keyframe.WithAspectRatio` | アスペクト比（キャラクターに一致する参照画像があればそれを優先します） |
| `keyframe.WithImageSize` | 解像度（既定 "2K"） |
| `keyframe.WithNegativePrompt` | ネガティブプロンプトの差し替え（既定は文字・フキダシ排除の定型文） |

`RateInterval` と `RequestTimeout` は `workflow` の `callGuard` が受け持ち、**台本のテキスト生成とキーフレームの画像生成の両方**に掛かります（クォータはプロジェクト単位で、操作の種類ごとではないため）。`MaxConcurrency` はカット間の並列度で、`keyframe.Generator` が `errgroup` で適用します — 1枚ごとの進捗ログ（何枚中の何枚目か・所要時間）を出すために、画像キット側の一括生成には委ねていません。なお `RateInterval` が設定されていれば、スループットは並列度によらず発射間隔で頭打ちになります。

保存側は `runner.NewCutKeyframeRunner` の `runner.WithCacheControl` で、キーフレーム画像に付ける `Cache-Control` を差し替えられます（既定は `DefaultKeyframeCacheControl` = `public, max-age=1800`。生成物を公開したくないデプロイでは `private` などを指定してください）。

キーフレームの生成結果はライブラリ自身の型 `video.KeyframeImage`（`Data` / `MimeType` / `UsedSeed` / `Model` / `Prompt`）で返ります。vertex-image-kit の型を公開面に出さないための境界で、利用側は画像キットを import せずに済みます。
