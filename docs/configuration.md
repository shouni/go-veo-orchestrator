# ⚙️ 設定と差し替え (Config / DI)

[← README](../README.md)

`workflow.New(ManagerArgs)` に渡す設定と依存です。

## ports.Config

**4 項目が必須**です（未設定なら `ports.ErrConfigInvalid`）。モデル名 2 種と、画作りの 2 種（アスペクト比・解像度）です。理由は下の「画作りの既定値は持ちません」を参照してください。実行制御（並列度・タイムアウト）はゼロ値で構いません（`ApplyDefaults` が補完します）。`RateInterval` だけは補完しません — 0 が「制限なし」という意味を持つ値だからです。

| フィールド | 役割 |
| --- | --- |
| `GeminiModel` | 台本生成に使うテキストモデル（**必須**） |
| `ImageModel` | キーフレーム画像生成に使うモデル（**必須**） |
| `MaxConcurrency` | キーフレーム生成の最大並列数（動画生成は Video-to-Video 連鎖のため常に逐次です） |
| `RateInterval` | AI 呼び出しの発射間隔の下限（0 で無制限）。台本のテキスト生成にも掛かります |
| `RequestTimeout` | AI 呼び出し 1 回あたりの上限時間（既定 5 分）|
| `KeyframeAspectRatio` | キーフレームのアスペクト比（例: `16:9`, `9:16`）。**必須** |
| `KeyframeImageSize` | キーフレームの出力解像度（例: `1K`, `2K`）。**必須** |
| `KeyframeNegativePrompt` | キーフレームで排除したい要素（例: `speech bubble, text, watermark`）。任意（空は「排除指定なし」）|

`Config.WithModels(gemini, image)` / `Config.WithAspectRatio(ratio)` で部分的に上書きしたコピーを取得できます（空文字は「変更なし」）。

## ManagerArgs

| フィールド | 役割 |
| --- | --- |
| `AIClient` | `gemini.Generator`（台本生成・キーフレーム生成）。生成呼び出しの 1 メソッドだけを要求します |
| `Reader` | `ports.ContentReader`。台本ソースの取得に使います（参照画像は `gs://` のまま渡るので、画像には使いません） |
| `Writer` | `remoteio.Writer`。**同時アクセス安全な実装が必要です** — キーフレームはカットごとの goroutine が生成直後に保存します |
| `VideoRunner` | Veo API アダプタ。未設定なら `ErrVideoRunnerNotConfigured` を返すダミーになります |
| `PromptDeps` | キャラクター定義とプロンプト実装（下記） |

## PromptDeps

プロンプトはこのライブラリに含めず、`PromptDeps` から注入します。

| フィールド | インターフェース | 役割 |
| --- | --- | --- |
| `Characters` | `*characterkit.Characters` | キャラクター定義。カットの `CharacterID` から解決し、未知IDは既定キャラクターへ落とします |
| `ScriptPrompt` | `ports.ScriptPrompt` | Music Recipe から動画台本を生成するプロンプト（`Build(mode, *TemplateData)`） |
| `KeyframePrompt` | `ports.KeyframePrompt` | カットのキーフレーム生成・編集のプロンプト（`BuildCut` / `BuildEdit`） |

## キーフレーム生成の内部オプション

`keyframe` と `runner` は `internal/` にあるため、消費側が直接触ることはありません。`workflow.New` が `Config` から次のように組み立てます。

| Config | 内部での使われ方 |
| --- | --- |
| `KeyframeAspectRatio` | アスペクト比（キャラクターに一致する参照画像があればそれを優先します） |
| `KeyframeImageSize` | 解像度 |
| `KeyframeNegativePrompt` | ネガティブプロンプト（未設定なら排除指定なし） |
| `MaxConcurrency` | カット間の並列度。`runner.CutKeyframeRunner` が `errgroup` で適用します |
| `RateInterval` / `RequestTimeout` | `workflow` の `callGuard` |

`RateInterval` と `RequestTimeout` は**台本のテキスト生成とキーフレームの画像生成の両方**に掛かります（クォータはプロジェクト単位で、操作の種類ごとではないため）。発射間隔の待機は 1 回あたりの上限時間の外側で行うので、混雑がタイムアウトに化けることはありません。

並列度が `keyframe` ではなく `runner` にあるのは、**保存も `runner` が持つ**ためです。1 カットの「生成 → 保存」を 1 つの goroutine で完結させることで、途中で落ちても保存済みの分が残ります。なお `RateInterval` が設定されていれば、スループットは並列度によらず発射間隔で頭打ちになります。

保存側の `Cache-Control` は `runner.WithCacheControl` で差し替えられます（既定は `public, max-age=1800`。生成物を公開したくないデプロイでは `private` などを指定してください）。

キーフレームの生成結果はライブラリ自身の型 `video.KeyframeImage`（`Data` / `MimeType` / `UsedSeed` / `Model` / `Prompt`）で扱います。vertex-image-kit の型を公開面に出さないための境界で、利用側は画像キットを import せずに済みます。

## 画作りの既定値は持ちません

`KeyframeAspectRatio` / `KeyframeImageSize` を必須にし、`KeyframeNegativePrompt` の既定文言も持たないのは意図的です。

これらは作品ごとに変わる値で、呼び出し側は自分の設定（ap-mv なら `VEO_ASPECT_RATIO`）を既に持っています。キットが既定値を併せ持つと出所が2つになり、片方だけ変えたときに黙って食い違います — ショート動画用に `9:16` を設定したのに、キーフレームだけキットの `16:9` で焼かれる、という形で。ログにも何も出ません。

ネガティブプロンプトをキットに置かないのは、そこに画風の指定が混ざるためです。文字やフキダシの排除は普遍的でも、`monochrome` / `black and white` はアートディレクションそのもので、モノクロで作りたい作品がキットのリリース無しには作れなくなります。
