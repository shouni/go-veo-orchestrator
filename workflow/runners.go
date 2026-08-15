package workflow

import (
	"github.com/shouni/go-veo-orchestrator/internal/keyframe"
	"github.com/shouni/go-veo-orchestrator/internal/runner"
	"github.com/shouni/go-veo-orchestrator/ports"
)

// buildAllRunners は、ワークフローの実行に必要なすべてのランナーを構築して返します。
func (m *manager) buildAllRunners() (*ports.Workflows, error) {
	sr := m.buildScriptRunner()
	keyframeR := m.buildKeyframeRunner()
	pubR := m.buildPublishRunner()
	videoR := m.buildVideoTimelineRunner()

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
		m.promptDeps.Characters,
		m.imageGenerator,
		m.promptDeps.KeyframePrompt,
		m.cfg.ImageModel,
		keyframe.WithAspectRatio(m.cfg.KeyframeAspectRatio),
		keyframe.WithImageSize(m.cfg.KeyframeImageSize),
		keyframe.WithNegativePrompt(m.cfg.KeyframeNegativePrompt),
	)

	return runner.NewCutKeyframeRunner(keyframeGen, m.writer,
		runner.WithMaxConcurrency(m.cfg.MaxConcurrency))
}

// buildPublishRunner は、動画メタデータのパブリッシュを担当する Runner を作成します。
func (m *manager) buildPublishRunner() *runner.VideoPublisherRunner {
	return runner.NewVideoPublisherRunner(m.writer)
}

// buildVideoTimelineRunner は、保存済みキーフレームを起点に Veo 生成を回す Runner を
// 作成します。キーフレームの生成・保存は CutKeyframeRunner の責務で、ここは関与しません。
// キャラクター定義が利用できる場合は、カットの立ち絵を referenceImages として渡す
// リクエストビルダーを使います。
func (m *manager) buildVideoTimelineRunner() ports.VideoTimelineRunner {
	if m.videoRunner == nil {
		return ports.NewNoopVideoTimelineRunner()
	}
	return runner.NewVideoTimelineRunner(m.videoRunner).
		WithRequestBuilder(runner.NewVideoRequestBuilderWithCharacters(m.promptDeps.Characters))
}
