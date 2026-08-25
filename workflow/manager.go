// Package workflow は、設定とクライアント群から go-veo-orchestrator の各 Runner
// （台本・キーフレーム・動画・パブリッシュ）を組み立てる DI 層を提供します。
// AI 呼び出しの発射間隔・1回あたりの上限時間・重複排除（singleflight）もここで掛けます
// （callGuard 参照）。
package workflow

import (
	"fmt"

	"github.com/shouni/gemini-image-kit/generator"
	imagePorts "github.com/shouni/gemini-image-kit/ports"
	characterkit "github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-gemini-client/callguard"
	"github.com/shouni/go-gemini-client/gemini"
	"github.com/shouni/go-remote-io/remoteio"

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
	// Writer は生成物とメタデータの保存先です。
	//
	// **同時アクセス安全である必要があります。** キーフレームはカットごとの goroutine が
	// 生成直後に保存するため、Config.MaxConcurrency が 2 以上なら Write は並行に呼ばれます。
	Writer remoteio.Writer
	// AIClient は台本のテキスト生成とキーフレームの画像生成の両方に使います。
	// 生成呼び出しの 1 メソッドだけを要求します。
	AIClient    gemini.Generator
	VideoRunner ports.VideoRunner
	PromptDeps  *PromptDeps
}

// manager は、ワークフローの各工程を担う Runner 群を構築・管理します。
type manager struct {
	cfg            ports.Config
	reader         ports.ContentReader
	writer         remoteio.Writer
	aiClient       gemini.Generator
	videoRunner    ports.VideoRunner
	imageGenerator imagePorts.ImageGenerator
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

	// 発射間隔と 1 回あたりの上限時間はワークフロー全体で 1 つのガードに集約する
	// （クォータはプロジェクト単位で、操作の種類ごとではないため）。
	guard := callguard.New(
		callguard.WithRateInterval(cfg.RateInterval),
		callguard.WithExecTimeout(cfg.RequestTimeout),
	)

	m := &manager{
		cfg:         cfg,
		reader:      args.Reader,
		writer:      args.Writer,
		aiClient:    &singleflightGenerator{inner: args.AIClient, guard: guard},
		videoRunner: args.VideoRunner,
		promptDeps:  args.PromptDeps,
	}

	imageGenerator, err := buildImageGenerator(args.AIClient, guard)
	if err != nil {
		return nil, err
	}
	m.imageGenerator = imageGenerator

	return m.buildAllRunners()
}

// validateArgs は引数のバリデーションを行います。
//
// 文面は ManagerArgs の実際のフィールド名で書きます。呼び出し側がメッセージを頼りに
// 直す場所を探すため、名前がずれると grep しても当たりません。
func validateArgs(args *ManagerArgs) error {
	if args.Reader == nil {
		return fmt.Errorf("ManagerArgs.Reader は必須です")
	}
	if args.Writer == nil {
		return fmt.Errorf("ManagerArgs.Writer は必須です")
	}
	if args.AIClient == nil {
		return fmt.Errorf("ManagerArgs.AIClient は必須です")
	}
	if args.PromptDeps == nil {
		return fmt.Errorf("ManagerArgs.PromptDeps は必須です")
	}
	if args.PromptDeps.Characters == nil {
		return fmt.Errorf("ManagerArgs.PromptDeps.Characters は必須です")
	}
	if args.PromptDeps.ScriptPrompt == nil {
		return fmt.Errorf("ManagerArgs.PromptDeps.ScriptPrompt は必須です")
	}
	if args.PromptDeps.KeyframePrompt == nil {
		return fmt.Errorf("ManagerArgs.PromptDeps.KeyframePrompt は必須です")
	}

	return nil
}

// buildImageGenerator は画像生成の実行体を組み立てます。
//
// 参照画像は gs:// URI をそのまま Vertex AI へ渡すため、解決経路は GCSResolver 単体で
// 足ります。この resolver は取得もアップロードもキャッシュもしないので、GCS リーダーも
// HTTP クライアントもキャッシュも渡しません。取得系の resolver を足したくなったら、
// それは参照が gs:// でなくなった合図です。
//
// generator.New にオプションを渡していないのも意図どおりで、発射間隔と上限時間は
// callGuard 側で掛けます（理由は singleflight.go の callGuard を参照）。
func buildImageGenerator(client gemini.Generator, guard *callguard.Guard) (imagePorts.ImageGenerator, error) {
	gen, err := generator.New(client, generator.NewGCSResolver())
	if err != nil {
		return nil, fmt.Errorf("画像生成エンジンの初期化に失敗しました: %w", err)
	}
	return &singleflightImageGenerator{inner: gen, guard: guard}, nil
}
