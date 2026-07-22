package workflow

import (
	"github.com/shouni/go-veo-orchestrator/keyframe"
	"github.com/shouni/go-veo-orchestrator/ports"
	"github.com/shouni/go-veo-orchestrator/runner"
)

// buildAllRunners は、ワークフローの実行に必要なすべてのランナーを構築して返します。
func (m *manager) buildAllRunners() (*ports.Workflows, error) {
	sr := m.buildScriptRunner()
	keyframeR := m.buildKeyframeRunner()
	pubR := m.buildPublishRunner()
	videoR := m.buildVideoTimelineRunner(keyframeR, pubR)

	return &ports.Workflows{
		Script:      sr,
		CutKeyframe: keyframeR,
		Video:       videoR,
		Publish:     pubR,
	}, nil
}

// buildScriptRunner は、台本生成を担当する Runner を作成します。
func (m *manager) buildScriptRunner() *runner.VideoScriptRunner {
	return runner.NewVideoScriptRunner(m.promptDeps.ScriptPrompt, m.aiClient, m.reader, m.cfg.GeminiModel, m.promptDeps.Characters)
}

// buildKeyframeRunner は、カットのキーフレーム画像生成を担当する Runner を作成します。
func (m *manager) buildKeyframeRunner() *runner.CutKeyframeRunner {
	keyframeGen := keyframe.NewGenerator(
		m.generationUnit.composer,
		m.generationUnit.imageGenerator,
		m.promptDeps.KeyframePrompt,
		m.generationUnit.model,
		keyframe.WithMaxConcurrency(m.cfg.MaxConcurrency),
		keyframe.WithRateInterval(m.cfg.RateInterval),
		keyframe.WithAspectRatio(m.cfg.KeyframeAspectRatio),
	)

	return runner.NewCutKeyframeRunner(keyframeGen, m.writer)
}

// buildPublishRunner は、動画メタデータのパブリッシュを担当する Runner を作成します。
func (m *manager) buildPublishRunner() *runner.VideoPublisherRunner {
	return runner.NewVideoPublisherRunner(m.writer)
}

// buildVideoTimelineRunner は、キーフレーム生成から Veo 生成までをつなぐ Runner を作成します。
// キャラクター定義が利用できる場合は、カットの立ち絵を referenceImages として渡す
// リクエストビルダーを使います。
func (m *manager) buildVideoTimelineRunner(
	keyframeRunner ports.CutKeyframeRunner,
	publisher ports.VideoPublishRunner,
) ports.VideoTimelineRunner {
	if m.videoRunner == nil {
		return ports.NewNoopVideoTimelineRunner()
	}
	return runner.NewVideoTimelineRunner(keyframeRunner, m.videoRunner, publisher).
		WithRequestBuilder(runner.NewVideoRequestBuilderWithCharacters(m.promptDeps.Characters))
}
