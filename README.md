# 🎨 Go Veo Orchestrator

[![Language](https://img.shields.io/badge/Language-Go-blue)](https://golang.org/)
[![Go Version](https://img.shields.io/github/go-mod/go-version/shouni/go-veo-orchestrator)](https://golang.org/)
[![GitHub tag (latest by date)](https://img.shields.io/github/v/tag/shouni/go-veo-orchestrator)](https://github.com/shouni/go-veo-orchestrator/tags)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Report Card](https://goreportcard.com/badge/github.com/shouni/go-veo-orchestrator)](https://goreportcard.com/report/github.com/shouni/go-veo-orchestrator)
[![Go Reference](https://pkg.go.dev/badge/github.com/shouni/go-veo-orchestrator.svg)](https://pkg.go.dev/github.com/shouni/go-veo-orchestrator)
[![Status](https://img.shields.io/badge/Status-In%20Development-yellow)](#)


## 🚀 概要 (About) - キャラクターDNA維持・マルチモーダル動画生成Orchestrator

**Go Veo Orchestrator** は、Music Recipe（音楽レシピ / 楽曲構成書）を解析し、Googleの次世代動画生成AIである **Veo (Vertex AI / Gemini API)** を用いて、**キャラクターのDNA（一貫性）を完全に維持した動画作品**を自動生成するための高度なバックエンドパイプラインです。

[Gemini Image Kit](https://github.com/shouni/gemini-image-kit) から派生した静止画生成コア技術を応用し、「プロンプト（テキスト）」「高精度静止画（キーフレーム）」「音源(mp3)」「直前の動画（コンテキスト）」の4大要素（**マルチモーダル・クアッド・インプット**）を時間軸に沿って統合管理します。

独自の **Video-to-Video 連鎖（数珠繋ぎ）生成アルゴリズム** により、シーンを跨いでもキャラクターの服装や容姿が崩れない商業クオリティのアニメーション・動画パイプラインの構築を実現します。

---

## ✨ コア・コンセプト (Core Concepts)

* **🧬 Quad-Factor Consistency Control (4要素協調制御)**:
  * 動画AI（Veo）における最大の課題である「カットごとの容姿の破綻」を防ぐため、**キャラクター固有Seed**、**PanelGen由来の画像（キーフレーム）**、**動きの言語指示**、そして**前カットの動画コンテキスト**を同期させて1つのリクエストを構築します。
* **⏳ Timeline-Based Layout Logic (時間軸管理)**:
  * 漫画の「コマ割り・レイアウト計算」を、動画の**「タイムライン・カット割り計算（秒数制御）」**へと再定義。何秒のカットをどう繋ぐかをプログラム側で決定論的にコントロールします。
* **🛡 Production-Ready Concurrency & Rate Control**:
  * セマフォ（Semaphore）を用いた細やかな並列実行制御に加え、大容量動画・音声アセットの通信を保護するため `singleflight` を活用。`RESOURCE_EXHAUSTED` (429) エラーや重複アップロードのオーバーヘッドを徹底的に排除します。
* **💾 Lean Data Architecture**:
  * HTMLなどのUI出力をバッサリと削ぎ落とし、純粋な動画データ（mp4）の結合処理と、タイムスタンプおよびメタデータ（JSON記述の動画プロット）の出力・保存に完全特化しています。

---

## 🎬 5つの動画生成ワークフロー (Workflows)

| ワークフロー | 担当インターフェース | 内容 |
| --- | --- | --- |
| **1. Designing** | `DesignRunner` | キャラクターのDNA（Seed/ビジュアル特徴）を固定し、一貫性の基盤となるデザインシートを定義。 |
| **2. Scripting** | `ScriptRunner` | 非構造化ドキュメントを解析し、シーン・カット割り・セリフ・カメラワーク・推定秒数を含むJSON動画台本を生成。 |
| **3. Cut Keyframe Gen** | `CutImageRunner` | 各カットのベースとなる高精度な「静止画（キーフレーム）」を、キャラ固有Seedを用いて個別に作画。 |
| **4. Video Orchestrate** | `VideoTimelineRunner` | キーフレーム画像、動きのプロンプト、音源(mp3)、前カットの動画IDをVeoへパイプラインし、連続するシーンを生成。 |
| **5. Transcoding & Plot** | `VideoPublishRunner` | 生成された複数のカット動画（mp4）を統合・構造化し、最終動画アセットおよびメタデータJSONとしてパブリッシュ。 |

---

## 📂 プロジェクト構造 (Project Structure)

本アーキテクチャは **ports による抽象化（Hexagonal Architecture）** を境界線としており、Veo API のエンドポイント変更や動画合成エンジンの差し替えを容易に行える設計を採用しています。

```text
go-veo-orchestrator/
├── workflow/    # 【統合管理】各工程を組み合わせ、Workflows (MangaToVideo) インターフェースを実装。
├── runner/      # 【実行実体】Design/Script/CutGen/VideoGen/Publish の具体的なプロセス実装。
├── timeline/    # 【生成戦略】秒数計算、カット連携、Video-to-Video の数珠繋ぎコンテキスト計算（※旧 layout）。
├── parser/      # 【解析】入力プロットやAIのマルチモーダルレスポンスを構造化データへ変換。
├── ports/       # 【契約・定義】Interface（VideoRunner等）、共通モデル、動作設定(Config)。全ての起点。
└── asset/       # 【アセット管理】大容量動画(mp4)・音声(mp3)・画像アセットのパス解決およびURIマッピング。
```

---

## 🔄 シーケンスフロー (Sequence Flow)

### Video Orchestration Flow (`NewVideoTimelineRunner`)

```mermaid
sequenceDiagram
  participant WF as workflow.manager
  participant VFactory as runner.NewVideoTimelineRunner
  participant LTime as timeline.NewTimelineGenerator
  participant Composer as timeline.VideoComposer
  participant VideoRunner as runner.VideoTimelineRunner
  participant CutGen as timeline.TimelineGenerator
  participant VeoAPI as Vertex AI (Veo API)
  participant Writer as remoteio.Writer

  Note over WF,LTime: 1) GenerationUnit / Video Runner 初期化
  WF->>Composer: NewVideoComposer(core, charactersMap)
  Composer-->>WF: *timeline.VideoComposer
  WF->>LTime: NewTimelineGenerator(composer, videoGenerator, videoPrompt, model, opts...)
  LTime-->>WF: *timeline.TimelineGenerator
  WF->>VFactory: NewVideoTimelineRunner(generator, writer)
  VFactory-->>WF: *VideoTimelineRunner

  Note over WF,VideoRunner: 2) Timeline / Cut 単位での数珠繋ぎ動画生成
  WF->>VideoRunner: Run(ctx, script) / RunAndSave(ctx, script, outputPath)
  VideoRunner->>CutGen: Execute(ctx, script.Cuts)
  CutGen->>Composer: PrepareCharacterResources(ctx, cuts)
  Composer-->>CutGen: Character Base URI (GCS / File API)

  Note over CutGen,VeoAPI: Loop内の Video-to-Video で前カットのコンテキスト（VideoID）を連鎖
  var lastVideoID = ""

  loop cuts / errgroup + rate limiter (Semaphore)
    CutGen->>CutGen: BuildVideoRequest(cut, character, lastVideoID)
    CutGen->>VeoAPI: GenerateVideo(Prompt + KeyframeImage + InputAudio(mp3) + lastVideoID + Seed)
    VeoAPI-->>CutGen: カット動画レスポンス (mp4 Data + Generated VideoID)
    CutGen->>CutGen: lastVideoID = currentResponse.VideoID
  end

  CutGen-->>VideoRunner: []*videoPorts.VideoResponse

  opt RunAndSave
    VideoRunner->>Writer: Write(ctx, indexedCutPath, videoData, remoteio.WithContentType("video/mp4"), ...)
    VideoRunner->>Writer: Write(ctx, video_meta.json, updatedVideoPlotJSON, remoteio.WithContentType("application/json"), ...)
    VideoRunner-->>WF: *ports.VideoPlotResponse
  end

```

---

### 🤝 依存関係 (Dependencies)

* [shouni/gemini-image-kit](https://github.com/shouni/gemini-image-kit) - 静止画・キーフレーム生成コア基盤

### 📜 ライセンス (License)

このプロジェクトは [MIT License](https://opensource.org/licenses/MIT) の下で非公開・クローズド開発用として運用、またはポートフォリオ契約に基づいてライセンスされます。
