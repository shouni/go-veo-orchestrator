package ports

import (
	"context"

	"github.com/shouni/go-veo-orchestrator/video"
)

// VideoRunner is the contract for calling the Veo API to generate and manage the
// video for one cut. The implementation (the Veo adapter) is not part of this
// repository; the caller injects it.
type VideoRunner interface {
	Run(ctx context.Context, req video.GenerationRequest) (*video.Response, error)
}

// ReferenceImagesSupporter is an optional VideoRunner interface that reports
// whether reference_to_video (referenceImages; supported only by non-Fast Veo 3
// models, and fixed at 8 seconds) is available. The code that normalizes cut
// durations to values Veo supports uses it to decide "will this cut be fixed at
// 8 seconds" without duplicating the model-detection logic.
//
// The adapter is what implements it, so it lives in this package, where the
// contracts for adapters are gathered, rather than next to
// veo.RunnerCapabilities, which assembles the decision.
type ReferenceImagesSupporter interface {
	SupportsReferenceImages() bool
}

// LastFrameSupporter is an optional VideoRunner interface that reports whether
// frames_to_video (first/last frame interpolation with image + lastFrame;
// supported only by Veo 2 / Veo 3.1 models) is available.
type LastFrameSupporter interface {
	SupportsLastFrame() bool
}

// VideoTimelineRunner is the contract for turning a recipe's cuts into video, one after another.
type VideoTimelineRunner interface {
	Run(ctx context.Context, r *video.Recipe) ([]*video.Response, error)
}

// noopVideoTimelineRunner is a VideoTimelineRunner that always fails with
// ErrVideoRunnerNotConfigured. When no VideoRunner (Veo adapter) is injected,
// Workflows.Video gets this instead of nil, so the caller receives an error it
// can check with errors.Is instead of a nil panic on first use.
type noopVideoTimelineRunner struct{}

// NewNoopVideoTimelineRunner returns a VideoTimelineRunner that always returns
// ErrVideoRunnerNotConfigured.
func NewNoopVideoTimelineRunner() VideoTimelineRunner {
	return noopVideoTimelineRunner{}
}

func (noopVideoTimelineRunner) Run(context.Context, *video.Recipe) ([]*video.Response, error) {
	return nil, ErrVideoRunnerNotConfigured
}
