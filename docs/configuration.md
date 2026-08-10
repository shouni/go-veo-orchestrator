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
| `RateBurst` | キーフレーム生成レート制限のバースト許容数（既定 1） |
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

並列数・レート制限は注入する画像ジェネレータ側（gemini-image-kit の `WithMaxConcurrency` / `WithRateLimit`）の設定で、`workflow` が `Config.MaxConcurrency` / `RateInterval` / `RateBurst` をそちらへ配線します。

保存側は `runner.NewCutKeyframeRunner` の `runner.WithCacheControl` で、キーフレーム画像に付ける `Cache-Control` を差し替えられます（既定は `DefaultKeyframeCacheControl` = `public, max-age=1800`。生成物を公開したくないデプロイでは `private` などを指定してください）。

キーフレームの生成結果はライブラリ自身の型 `ports.KeyframeImage`（`Data` / `MimeType` / `UsedSeed` / `Model` / `Prompt`）で返ります。gemini-image-kit の型を公開面に出さないための境界で、利用側は画像キットを import せずに済みます。
