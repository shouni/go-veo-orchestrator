# 🔌 Adapter Boundary — VideoRunner の実装

[← README](../README.md)

このリポジトリは Veo API クライアントではなく、Veo に渡すための **ドメインモデル、キーフレーム生成、リクエスト構築、Video-to-Video 連鎖、メタデータ保存** を担当する orchestration ライブラリです。

Veo API への実通信は `ports.VideoRunner` の実装として、利用側アプリケーションまたは別パッケージから差し込みます。このリポジトリ内には本番用 Veo adapter は含めず、実行環境ごとの差分を adapter 側に閉じ込めます。

## adapter が担う責務

* Google Cloud / Vertex AI / Gemini API などの認証
* `ImageReference` / `AudioReference` の解決
* `InputImage` / `InputAudio` を使う場合のアップロードと参照 URI 化
* Veo API への動画生成リクエスト送信
* 長時間 operation のポーリング、タイムアウト、リトライ
* 生成動画の保存先管理
* 次カットへ引き継ぐための `VideoResponse.VideoID` 返却
* 参照可能な `VideoResponse.CloudURL` 返却

adapter 実装では `VideoGenerationRequest.ImageReference` を優先し、空の場合だけ `InputImage` をアップロードして参照 URI を作る想定です。`AudioReference` も同様に、参照 URI がある場合はそれを優先し、必要に応じて `InputAudio` をアップロードします。

キーフレーム生成の実体は `ports.CutImageGenerator`（`Execute` / 任意で `EditCut`）です。`EditCut` を実装していないエンジンで `EditAndSave` を呼ぶと `ErrEditingNotSupported` になります。

## 実装例

```go
type VeoRunner struct {
	// client, bucket, model, location など、実行環境に必要な依存を保持します。
}

func (r *VeoRunner) Run(ctx context.Context, req ports.VideoGenerationRequest) (*ports.VideoResponse, error) {
	// 1. req.ImageReference / req.AudioReference を優先して参照を解決
	// 2. 必要なら req.InputImage / req.InputAudio をアップロード
	// 3. req.Prompt, req.PreviousVideoURI, req.Seed, req.DurationSec を Veo API に渡す
	// 4. operation を poll して完了を待つ
	// 5. CloudURL と VideoID を返す
	return &ports.VideoResponse{
		CloudURL:    "gs://example-bucket/videos/cut_001.mp4",
		VideoID:     "veo-video-id",
		CutIndex:    req.CutIndex,
		DurationSec: req.DurationSec,
		MimeType:    "video/mp4",
	}, nil
}
```

## VideoGenerationRequest の契約

| フィールド | adapter 側の扱い |
| --- | --- |
| `Prompt` | Veo に渡す最終プロンプト。カット内容、カメラワーク、音楽同期指示を含みます。 |
| `ImageReference` | 既に参照可能なキーフレーム画像 URI。存在する場合は `InputImage` より優先します。 |
| `InputImage` | `ImageReference` が空の場合に adapter 側でアップロードして使う画像バイト列です。 |
| `AudioReference` | 既に参照可能な音声セグメント URI。 |
| `InputAudio` | `AudioReference` が空の場合に adapter 側でアップロードして使う音声バイト列です。 |
| `PreviousVideoURI` | 前カットの文脈を引き継ぐための ID。空の場合はチェーンなしで生成します。 |
| `LastFrameReference` | 終了フレームとして使う画像 URI（Veo の first/last frame 補間）。Veo API では開始フレーム画像との併用が必須のため、image 入力（image_to_video）のときだけ `lastFrame` として送ります。対応モデルは Veo 2 / Veo 3.1 系のみです。 |
| `Seed` | キャラクター Seed を優先し、未指定時は `music_recipe.Seed` を使います。 |
| `CutIndex` | レスポンスやエラー表示で使うカット番号です。 |
| `DurationSec` | カットの目標秒数です。 |

`VideoResponse.VideoID` が空の場合、そのカットの生成結果は保存できますが、次カットへの `PreviousVideoURI` 連鎖は更新されません。連続カットの一貫性を重視する adapter では、可能な限り Veo 側の動画 ID を返してください。

> **`PreviousVideoURI` は `gs://` URI の契約です。** `gs://` で始まらない値は video_extension として分類されないため、operation 名や署名付き URL を入れた adapter はチェーン全体を黙って image_to_video へ格下げします。

## VideoRunner を渡さない場合

`workflow.New` が返す `Workflows.Video` は `nil` ではなく、呼び出すと常に `ports.ErrVideoRunnerNotConfigured` を返すダミー実装（`ports.NewNoopVideoTimelineRunner()`）になります。Script / CutKeyframe / Publish だけを使う構成ではそのまま利用できます。

未設定を検知したい場合は `errors.Is(err, ports.ErrVideoRunnerNotConfigured)` で判定してください（`Workflows.Video == nil` によるチェックは機能しません）。

## モデルの対応機能を伝える

どの Veo 機能が使えるかは、`VideoRunner` に以下のオプションインターフェースを実装すると `ports.RunnerCapabilities` が自動で拾います。未実装の Runner は両方 false となり、image_to_video 側へ倒れます。

```go
type ReferenceImagesSupporter interface{ SupportsReferenceImages() bool }
type LastFrameSupporter      interface{ SupportsLastFrame() bool }
```

分類の詳細は [Veo 生成モードとカット尺](veo-modes.md) を参照してください。
