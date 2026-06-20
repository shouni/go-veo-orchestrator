package workflow

import (
	"fmt"

	"github.com/shouni/gemini-image-kit/generator"
	characterkit "github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-gemini-client/gemini"

	"github.com/shouni/go-veo-orchestrator/keyframe"
)

// buildGenerationUnit は画像生成 core、composer、generator をまとめた内部ユニットを構築します。
func (m *manager) buildGenerationUnit(client gemini.GenerativeModel, modelName string) (*generationUnit, error) {
	core, err := m.buildCore(client)
	if err != nil {
		return nil, err
	}

	composer, err := m.buildComposer(core, m.promptDeps.Characters)
	if err != nil {
		return nil, err
	}

	gen, err := m.buildGenerator(core)
	if err != nil {
		return nil, err
	}

	return &generationUnit{
		imageGenerator: gen,
		composer:       composer,
		model:          modelName,
	}, nil
}

// buildCore は GeminiImageCore エンジンを初期化します。
func (m *manager) buildCore(aiClient gemini.GenerativeModel) (*generator.GeminiImageCore, error) {
	core, err := generator.NewGeminiImageCore(
		aiClient,
		m.reader,
		m.httpClient,
		newImageCache(),
		defaultTTL,
		false,
	)
	if err != nil {
		return nil, fmt.Errorf("画像生成エンジンの初期化に失敗しました: %w", err)
	}

	return core, nil
}

// buildComposer はキャラクターリソースを扱う Composer を初期化します。
func (m *manager) buildComposer(
	core *generator.GeminiImageCore,
	chars *characterkit.Characters,
) (*keyframe.Composer, error) {
	composer, err := keyframe.NewComposer(
		core,
		core,
		chars,
	)
	if err != nil {
		return nil, fmt.Errorf("Composerの初期化に失敗しました: %w", err)
	}

	return composer, nil
}

// buildGenerator は画像生成を実行する GeminiGenerator を初期化します。
func (m *manager) buildGenerator(core *generator.GeminiImageCore) (*generator.GeminiGenerator, error) {
	gen, err := generator.NewGeminiGenerator(core)
	if err != nil {
		return nil, fmt.Errorf("GeminiGeneratorの初期化に失敗しました: %w", err)
	}

	return gen, nil
}
