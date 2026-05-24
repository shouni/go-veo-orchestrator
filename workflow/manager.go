package workflow

import (
	"fmt"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	"github.com/shouni/go-gemini-client/gemini"
	"github.com/shouni/go-http-kit/httpkit"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-veo-orchestrator/layout"
	"github.com/shouni/go-veo-orchestrator/ports"
)

// PromptDeps はプロンプト関連の依存関係をまとめた構造体です。
type PromptDeps struct {
	CharactersMap ports.CharactersMap
	ScriptPrompt  ports.ScriptPrompt
	ImagePrompt   ports.ImagePrompt
}

// ManagerArgs は、ワークフローの初期化と管理に必要な引数の集合を表します。
type ManagerArgs struct {
	Config          ports.Config
	HTTPClient      httpkit.HTTPClient
	Reader          ports.ContentReader
	Writer          remoteio.Writer
	AIClient        gemini.GenerativeModel
	AIClientQuality gemini.GenerativeModel
	VideoRunner     ports.VideoRunner
	PromptDeps      *PromptDeps
}

// generationUnit は、画像生成と構成を処理するユニットを表します
type generationUnit struct {
	imageGenerator imagePorts.ImageGenerator
	mangaComposer  *layout.MangaComposer
	model          string
}

// layoutManager は、レイアウトの生成単位を管理します
type layoutManager struct {
	Standard *generationUnit
	Quality  *generationUnit
}

// manager は、ワークフローの各工程を担う Runner 群を構築・管理します。
type manager struct {
	cfg             ports.Config
	httpClient      httpkit.HTTPClient
	reader          ports.ContentReader
	writer          remoteio.Writer
	aiClient        gemini.GenerativeModel
	aiClientQuality gemini.GenerativeModel
	videoRunner     ports.VideoRunner
	layoutManager   layoutManager
	promptDeps      *PromptDeps
}

// New は、設定とキャラクター定義を基に新しい Workflows を初期化します。
func New(args ManagerArgs) (*ports.Workflows, error) {
	if err := validateArgs(&args); err != nil {
		return nil, err
	}

	cfg := args.Config
	cfg.ApplyDefaults()

	aiClientQuality := args.AIClientQuality
	if aiClientQuality == nil {
		aiClientQuality = args.AIClient
	}

	m := &manager{
		cfg:             cfg,
		httpClient:      args.HTTPClient,
		reader:          args.Reader,
		writer:          args.Writer,
		aiClient:        args.AIClient,
		aiClientQuality: aiClientQuality,
		videoRunner:     args.VideoRunner,
		promptDeps:      args.PromptDeps,
	}

	var err error

	m.layoutManager.Standard, err = m.buildGenerationUnit(m.aiClient, cfg.ImageStandardModel)
	if err != nil {
		return nil, fmt.Errorf("standard GenerationUnit の構築に失敗: %w", err)
	}

	m.layoutManager.Quality, err = m.buildGenerationUnit(m.aiClientQuality, cfg.ImageQualityModel)
	if err != nil {
		return nil, fmt.Errorf("quality GenerationUnit の構築に失敗: %w", err)
	}

	return m.buildAllRunners()
}

// validateArgs は引数のバリデーションを行います。
func validateArgs(args *ManagerArgs) error {
	if args.HTTPClient == nil {
		return fmt.Errorf("HTTPClient is required")
	}
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
	if args.PromptDeps.CharactersMap == nil {
		return fmt.Errorf("CharactersMap is required")
	}
	if args.PromptDeps.ScriptPrompt == nil {
		return fmt.Errorf("ScriptPrompt is required")
	}
	if args.PromptDeps.ImagePrompt == nil {
		return fmt.Errorf("ImagePrompt is required")
	}

	return nil
}
