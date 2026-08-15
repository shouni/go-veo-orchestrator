# 🎬 Go Veo Orchestrator

[![CI](https://github.com/shouni/go-veo-orchestrator/actions/workflows/ci.yml/badge.svg)](https://github.com/shouni/go-veo-orchestrator/actions/workflows/ci.yml)
[![Language](https://img.shields.io/badge/Language-Go-blue)](https://golang.org/)
[![Go Version](https://img.shields.io/github/go-mod/go-version/shouni/go-veo-orchestrator)](https://golang.org/)
[![GitHub tag (latest by date)](https://img.shields.io/github/v/tag/shouni/go-veo-orchestrator)](https://github.com/shouni/go-veo-orchestrator/tags)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/shouni/go-veo-orchestrator.svg)](https://pkg.go.dev/github.com/shouni/go-veo-orchestrator)

## 🚀 概要 (About)

**Go Veo Orchestrator** は、**Music Recipe（音楽レシピ / 楽曲構成書）** から動画カット列を構造化し、Google の動画生成 AI **Veo (Vertex AI / Gemini API)** へ渡すためのバックエンドオーケストレーターです。

[Vertex Image Kit](https://github.com/shouni/vertex-image-kit) を使ってカットごとのキーフレームを生成し、`VideoRunner` adapter を通じて Veo に **Prompt / Keyframe / Audio / PreviousVideoURI / Seed** を渡します。

`video_id` を次カットの `PreviousVideoURI` として引き継ぐことで、Video-to-Video の文脈を保った連続カット生成を行います。

---

## ✨ コア・コンセプト (Core Concepts)

* **🧬 Consistency Control**
  **キャラクター固有 Seed**、**キーフレーム画像**、**動きのプロンプト**、**前カットの VideoID** を 1 つの `video.GenerationRequest` にまとめ、カット間の見た目と文脈を維持します。

* **⏳ Audio-Driven Timeline Logic（音楽主導のタイムライン管理）**
  `music_recipe.sections` または `cuts` から `duration_sec`、`start_sec`、`end_sec` を補完し、`audio_cue` を Veo 用プロンプトへ注入します。

* **🔁 Resumable Video Chain**
  各 `cut` は `status`、`video_id`、`video_url` を保持します。生成済みカットは再生成せず、保持済み `video_id` を次カットの `PreviousVideoURI` として使用します。
  キーフレームも同じ考え方で、`keyframe_reference` を持つカットは焼き直しません（`CutKeyframe.GenerateAndSave`）。**1 枚生成するたびに保存する**ため、途中で落ちても失うのは最大 1 枚で、続きから再開できます。焼き直したいカットは `keyframe_reference` を空にしてから渡します（`Cut.ResetGeneration(false)`）。

* **🧩 Adapter-Oriented Architecture**
  Veo への実通信は `ports.VideoRunner` に閉じ込め、オーケストレーション、キーフレーム生成、メタデータ保存を分離しています。**Veo API の具体実装はこのリポジトリに含まれません**（[実装ガイド](docs/adapter.md)）。

---

## 🎬 4つの動画生成ワークフロー (Workflows)

| ワークフロー | 担当インターフェース | 内容 |
| --- | --- | --- |
| **1. Scripting** | `ScriptRunner` | Music Recipe JSON を読み込み、歌詞・section・楽曲展開から、カット割り・カメラワーク・推定秒数を含む **Video Recipe** を生成。 |
| **2. Cut Keyframe Gen** | `CutKeyframeRunner` | 各カットのキーフレーム画像を、キャラクター Seed と参照画像を使って生成・保存（`GenerateAndSave`）。既存キーフレームの局所編集にも対応（`EditAndSave`）。 |
| **3. Video Gen** | `VideoTimelineRunner` + `VideoRunner` | `VideoRequestBuilder` が `VideoGenerationRequest` を組み立て、Veo adapter に順次投入。 |
| **4. Metadata Publish** | `VideoPublishRunner` | `video_id` / `video_url` / `status` 更新済みの `video_music_meta.json` を保存。 |

---

## ⚡ クイックスタート

`workflow.New` に依存を渡して `*ports.Workflows` を組み立て、各 Runner を呼びます。

```go
workflows, err := workflow.New(workflow.ManagerArgs{
	// 5 項目とも必須です。キットはモデル名も画作りの既定値も持ちません。
	Config: ports.Config{
		GeminiModel:            "gemini-3.6-flash",
		ImageModel:             "gemini-3.1-flash-image",
		KeyframeAspectRatio:    "16:9",
		KeyframeImageSize:      "2K",
		KeyframeNegativePrompt: "speech bubble, text, watermark", // 任意
	},
	Reader:      reader, // 台本ソースの取得に使います（画像には使いません）
	Writer:      writer, // 並列保存するため、同時アクセス安全な実装が必要です
	AIClient:    geminiClient,
	VideoRunner: &VeoRunner{}, // 自前の Veo アダプタ
	PromptDeps:  promptDeps,
})
if err != nil {
	return err
}

// キーフレームを生成・保存してから動画を生成します。Video はキーフレームを
// 作りません（保存済みの keyframe_reference を読むだけです）。
if _, err := workflows.CutKeyframe.GenerateAndSave(ctx, recipe, "gs://bucket/jobs/<jobID>/"); err != nil {
	return err
}

videos, err := workflows.Video.Run(ctx, recipe)
if err != nil {
	return err
}

// メタデータの保存は Publish が担当します。動画生成と保存を分けているのは、
// 呼び出し側が生成と保存の間に処理（チェーンの結合など）を挟めるようにするためです。
if _, err := workflows.Publish.Run(ctx, recipe, "gs://bucket/jobs/<jobID>/"); err != nil {
	return err
}
```

---

## 📚 ドキュメント

| ドキュメント | 内容 |
| --- | --- |
| [Music Recipe JSON](docs/music-recipe.md) | 入力フォーマット、`cuts` の自動生成、`section_index`、`video.Cut` の構造 |
| [設定と差し替え (Config / DI)](docs/configuration.md) | `ports.Config` / `ManagerArgs` / `PromptDeps`、キーフレーム生成オプション |
| [Adapter Boundary](docs/adapter.md) | `ports.VideoRunner` の実装ガイドと `VideoGenerationRequest` の契約 |
| [Veo 生成モードとカット尺](docs/veo-modes.md) | `veo.ClassifyRequest` による分類、モード別の対応尺、尺プランナー |
| [レシピ・カットの操作](docs/recipe-api.md) | 再開・再生成のヘルパー、部分結果、単一カットのキーフレーム編集 |
| [Sentinel Errors](docs/errors.md) | `errors.Is` で分岐するためのエラー一覧 |
| [アーキテクチャ](docs/architecture.md) | パッケージ構成、生成と保存の責務分担、シーケンス図 |

---

## 🤝 依存関係 (Dependencies)

* [shouni/vertex-image-kit](https://github.com/shouni/vertex-image-kit) - Vertex AI 上の静止画・キーフレーム生成コア基盤（参照画像は `gs://` URI のまま渡します）
* [shouni/go-gemini-client](https://github.com/shouni/go-gemini-client) - Gemini API / Vertex AI クライアント（台本生成の構造化出力に使用）
* [shouni/go-character-kit](https://github.com/shouni/go-character-kit) - キャラクター資産（characters.json）管理
* [shouni/go-remote-io](https://github.com/shouni/go-remote-io) - GCS / ローカル対応の読み書き抽象化
* [shouni/go-utils](https://github.com/shouni/go-utils) - 共通ユーティリティ

## 📜 ライセンス (License)

このプロジェクトは [MIT License](https://opensource.org/licenses/MIT) の下で公開されています。
