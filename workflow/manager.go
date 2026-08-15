// Package workflow は、設定とクライアント群から go-veo-orchestrator の各 Runner
// （台本・キーフレーム・動画・パブリッシュ）を組み立てる DI 層を提供します。
// AI 呼び出しの発射間隔・1回あたりの上限時間・重複排除（singleflight）もここで
// 掛けます。テキスト生成と画像生成の両方に同じガードが必要だからです。
package workflow

import (
	"fmt"

	characterkit "github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-gemini-client/gemini"
	"github.com/shouni/go-remote-io/remoteio"
	imagePorts "github.com/shouni/vertex-image-kit/ports"

	"github.com/shouni/go-veo-orchestrator/ports"
)

// PromptDeps はプロンプト関連の依存関係をまとめた構造体です。
type PromptDeps struct {
	Characters     *characterkit.Characters
	ScriptPrompt   ports.ScriptPrompt
	KeyframePrompt ports.KeyframePrompt
}

// ManagerArgs は、ワークフローの初期化と管理に必要な引数の集合を表します。
type ManagerArgs struct {
	Config ports.Config
	// Reader は台本ソース（URL・テキスト）の取得に使います。参照画像は gs:// URI の
	// まま Vertex AI へ渡るため、画像の解決には使いません。
	Reader ports.ContentReader
	Writer remoteio.Writer
	// AIClient は台本のテキスト生成とキーフレームの画像生成の両方に使います。
	// 生成呼び出しの 1 メソッドだけを要求します。
	AIClient    gemini.Generator
	VideoRunner ports.VideoRunner
	PromptDeps  *PromptDeps
}

// generationUnit は画像生成とリソース構成をまとめた内部ユニットです。
type generationUnit struct {
	imageGenerator imagePorts.ImageGenerator
	model          string
}

// manager は、ワークフローの各工程を担う Runner 群を構築・管理します。
type manager struct {
	cfg            ports.Config
	reader         ports.ContentReader
	writer         remoteio.Writer
	aiClient       gemini.Generator
	videoRunner    ports.VideoRunner
	generationUnit *generationUnit
	promptDeps     *PromptDeps
}

// New は、設定とキャラクター定義を基に新しい Workflows を初期化します。
func New(args ManagerArgs) (*ports.Workflows, error) {
	if err := validateArgs(&args); err != nil {
		return nil, err
	}

	cfg := args.Config
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	// AI 呼び出しの発射間隔と1回あたりの上限時間は、ワークフロー全体で1つの
	// ガードに集約する（クォータはプロジェクト単位で、操作の種類ごとではないため）。
	guard := callGuard{
		limiter: newRateLimiter(cfg.RateInterval),
		timeout: cfg.RequestTimeout,
	}

	m := &manager{
		cfg:    cfg,
		reader: args.Reader,
		writer: args.Writer,
		// 同一内容のテキスト生成の同時実行を1回にまとめる（重複タスク・リトライ対策）
		aiClient:    &singleflightGenerator{inner: args.AIClient, guard: guard},
		videoRunner: args.VideoRunner,
		promptDeps:  args.PromptDeps,
	}

	unit, err := m.buildGenerationUnit(args.AIClient, cfg.ImageModel, guard)
	if err != nil {
		return nil, fmt.Errorf("GenerationUnit の構築に失敗: %w", err)
	}
	m.generationUnit = unit

	return m.buildAllRunners()
}

// validateArgs は引数のバリデーションを行います。
func validateArgs(args *ManagerArgs) error {
	if args.Reader == nil {
		return fmt.Errorf("InputReader is required")
	}
	if args.Writer == nil {
		return fmt.Errorf("OutputWriter is required")
	}
	if args.AIClient == nil {
		return fmt.Errorf("AIClient is required")
	}
	if args.PromptDeps == nil {
		return fmt.Errorf("PromptDeps is required")
	}
	if args.PromptDeps.Characters == nil {
		return fmt.Errorf("characters is required")
	}
	if args.PromptDeps.ScriptPrompt == nil {
		return fmt.Errorf("ScriptPrompt is required")
	}
	if args.PromptDeps.KeyframePrompt == nil {
		return fmt.Errorf("KeyframePrompt is required")
	}

	return nil
}
