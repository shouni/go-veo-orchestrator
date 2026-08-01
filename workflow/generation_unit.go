package workflow

import (
	"fmt"

	"github.com/shouni/gemini-image-kit/generator"
	"github.com/shouni/go-gemini-client/gemini"
)

// buildGenerationUnit は画像生成 core と generator をまとめた内部ユニットを構築します。
func (m *manager) buildGenerationUnit(client gemini.MultimodalModel, modelName string) (*generationUnit, error) {
	core, err := m.buildCore(client)
	if err != nil {
		return nil, err
	}

	gen, err := m.buildGenerator(core)
	if err != nil {
		return nil, err
	}

	return &generationUnit{
		imageGenerator: gen,
		model:          modelName,
	}, nil
}

// buildCore は GeminiImageCore エンジンを初期化します。
func (m *manager) buildCore(aiClient gemini.MultimodalModel) (*generator.GeminiImageCore, error) {
	// キャッシュを manager に保持し、Workflows.Close から Stop できるようにする。
	m.imageCache = newImageCache()
	core, err := generator.NewGeminiImageCore(generator.GeminiImageCoreConfig{
		AIClient:   aiClient,
		Reader:     m.reader,
		HTTPClient: m.httpClient,
		Cache:      m.imageCache,
		CacheTTL:   defaultTTL,
	})
	if err != nil {
		return nil, fmt.Errorf("画像生成エンジンの初期化に失敗しました: %w", err)
	}

	return core, nil
}

// buildGenerator は画像生成を実行する GeminiGenerator を初期化します。
func (m *manager) buildGenerator(core *generator.GeminiImageCore) (*generator.GeminiGenerator, error) {
	gen, err := generator.NewGeminiGenerator(core)
	if err != nil {
		return nil, fmt.Errorf("GeminiGeneratorの初期化に失敗しました: %w", err)
	}

	return gen, nil
}
