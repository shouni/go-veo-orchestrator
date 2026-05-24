package workflow

import (
	"fmt"

	"github.com/shouni/go-veo-orchestrator/layout"
	"github.com/shouni/go-veo-orchestrator/ports"
	"github.com/shouni/go-veo-orchestrator/runner"
)

// buildAllRunners は、ワークフローの実行に必要なすべてのランナーを構築して返します。
func (m *manager) buildAllRunners() (*ports.Workflows, error) {
	dr, err := m.buildDesignRunner()
	if err != nil {
		return nil, fmt.Errorf("DesignRunner のビルドに失敗しました: %w", err)
	}
	sr, err := m.buildScriptRunner()
	if err != nil {
		return nil, fmt.Errorf("ScriptRunner のビルドに失敗しました: %w", err)
	}
	panR, err := m.buildPanelImageRunner()
	if err != nil {
		return nil, fmt.Errorf("PanelImageRunner のビルドに失敗しました: %w", err)
	}
	pubR, err := m.buildPublishRunner()
	if err != nil {
		return nil, fmt.Errorf("PublishRunner のビルドに失敗しました: %w", err)
	}
	videoR := m.buildVideoTimelineRunner(panR, pubR)

	return &ports.Workflows{
		Design:      dr,
		Script:      sr,
		CutKeyframe: panR,
		Video:       videoR,
		Publish:     pubR,
		PanelImage:  panR,
	}, nil
}

// buildScriptRunner は、台本生成を担当する Runner を作成します。
func (m *manager) buildScriptRunner() (*runner.VideoScriptRunner, error) {
	return runner.NewVideoScriptRunner(m.promptDeps.ScriptPrompt, m.aiClient, m.reader, m.cfg.GeminiModel), nil
}

// buildDesignRunner は、キャラクターデザインを担当する Runner を作成します。
func (m *manager) buildDesignRunner() (*runner.MangaDesignRunner, error) {
	quality := m.layoutManager.Quality
	return runner.NewMangaDesignRunner(
		quality.mangaComposer,
		quality.imageGenerator,
		m.writer,
		quality.model,
		m.cfg.StyleSuffix,
	), nil
}

// buildPanelImageRunner は、パネル画像生成を担当する Runner を作成します。
func (m *manager) buildPanelImageRunner() (*runner.MangaPanelRunner, error) {
	standard := m.layoutManager.Standard
	panelsGen := layout.NewPanelGenerator(
		standard.mangaComposer,
		standard.imageGenerator,
		m.promptDeps.ImagePrompt,
		standard.model,
		layout.WithPanelMaxConcurrency(m.cfg.MaxConcurrency),
		layout.WithPanelRateInterval(m.cfg.RateInterval),
	)

	return runner.NewMangaPanelRunner(panelsGen, m.writer), nil
}

// buildPublishRunner は、動画メタデータのパブリッシュを担当する Runner を作成します。
func (m *manager) buildPublishRunner() (*runner.VideoPublisherRunner, error) {
	return runner.NewVideoPublisherRunner(m.writer), nil
}

// buildVideoTimelineRunner は、キーフレーム生成から Veo 生成までをつなぐ Runner を作成します。
func (m *manager) buildVideoTimelineRunner(
	keyframeRunner ports.CutKeyframeRunner,
	publisher ports.VideoPublishRunner,
) ports.VideoTimelineRunner {
	if m.videoRunner == nil {
		return nil
	}
	return runner.NewVideoTimelineRunner(keyframeRunner, m.videoRunner, publisher)
}
