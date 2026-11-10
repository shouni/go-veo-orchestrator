package workflow

import (
	"github.com/shouni/go-veo-orchestrator/internal/keyframe"
	"github.com/shouni/go-veo-orchestrator/ports"
	"github.com/shouni/go-veo-orchestrator/runner"
)

// buildAllRunners builds and returns every runner needed to run the workflows.
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

// buildScriptRunner creates the Runner responsible for script generation.
func (m *manager) buildScriptRunner() *runner.VideoScriptRunner {
	return runner.NewVideoScriptRunner(m.promptDeps.ScriptPrompt, m.aiClient, m.reader, m.cfg.GeminiModel, m.promptDeps.Characters)
}

// buildKeyframeRunner creates the Runner responsible for generating cut keyframe images.
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

// buildPublishRunner creates the Runner responsible for publishing video metadata.
func (m *manager) buildPublishRunner() *runner.VideoPublisherRunner {
	return runner.NewVideoPublisherRunner(m.writer)
}

// buildVideoTimelineRunner creates the Runner that drives Veo generation starting
// from the saved keyframes. Generating and saving keyframes is CutKeyframeRunner's
// job, and this one takes no part in it. When character definitions are available,
// it uses a request builder that passes each cut's character art as
// referenceImages.
func (m *manager) buildVideoTimelineRunner() ports.VideoTimelineRunner {
	if m.videoRunner == nil {
		return ports.NewNoopVideoTimelineRunner()
	}
	return runner.NewVideoTimelineRunner(m.videoRunner).
		WithRequestBuilder(runner.NewVideoRequestBuilderWithCharacters(m.promptDeps.Characters))
}
