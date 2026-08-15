package veo

import (
	"context"
	"testing"

	"github.com/shouni/go-veo-orchestrator/video"
)

// TestClassifyVeoRequest verifies the single source of truth for Veo request classification:
// video context (usePreviousVideo + gs:// previous video) wins and drops image references;
// otherwise referenceImages with a supporting model means reference_to_video; an image-input
// request with a last-frame reference and a supporting model means frames_to_video (Veo's
// lastFrame is only valid alongside the image start frame); everything else (no refs,
// unsupported model, non-gs:// video ID, missing start image) is image_to_video.
func TestClassifyVeoRequest(t *testing.T) {
	refs := []string{"gs://bucket/char.png", "gs://bucket/keyframe.png"}
	full := Capabilities{ReferenceImages: true, LastFrame: true}
	lastFrameOnly := Capabilities{LastFrame: true}
	none := Capabilities{}

	tests := []struct {
		name             string
		usePreviousVideo bool
		req              video.GenerationRequest
		caps             Capabilities
		want             GenerationMode
	}{
		{"video context wins over refs", true, video.GenerationRequest{PreviousVideoURI: "gs://bucket/prev.mp4", ReferenceImages: refs}, full, ModeVideoExtension},
		{"video context wins over last frame", true, video.GenerationRequest{PreviousVideoURI: "gs://bucket/prev.mp4", ImageReference: "gs://bucket/kf.png", LastFrameReference: "gs://bucket/next.png"}, full, ModeVideoExtension},
		{"previous video disabled", false, video.GenerationRequest{PreviousVideoURI: "gs://bucket/prev.mp4", ReferenceImages: refs}, full, ModeReferenceToVideo},
		{"non-gs previous video id", true, video.GenerationRequest{PreviousVideoURI: "operation-id-123", ReferenceImages: refs}, full, ModeReferenceToVideo},
		{"refs with supporting model", false, video.GenerationRequest{ReferenceImages: refs}, full, ModeReferenceToVideo},
		{"refs win over last frame", false, video.GenerationRequest{ReferenceImages: refs, ImageReference: "gs://bucket/kf.png", LastFrameReference: "gs://bucket/next.png"}, full, ModeReferenceToVideo},
		{"blank-only refs ignored", false, video.GenerationRequest{ReferenceImages: []string{" ", ""}}, full, ModeImageToVideo},
		{"refs without model support", false, video.GenerationRequest{ReferenceImages: refs}, none, ModeImageToVideo},
		{"no refs", false, video.GenerationRequest{}, full, ModeImageToVideo},
		{"last frame with image reference", false, video.GenerationRequest{ImageReference: "gs://bucket/kf.png", LastFrameReference: "gs://bucket/next.png"}, lastFrameOnly, ModeFramesToVideo},
		{"last frame with inline image", false, video.GenerationRequest{InputImage: []byte{0x89}, LastFrameReference: "gs://bucket/next.png"}, lastFrameOnly, ModeFramesToVideo},
		{"last frame on ref fallback", false, video.GenerationRequest{ReferenceImages: refs, ImageReference: "gs://bucket/kf.png", LastFrameReference: "gs://bucket/next.png"}, lastFrameOnly, ModeFramesToVideo},
		{"last frame without start image", false, video.GenerationRequest{LastFrameReference: "gs://bucket/next.png"}, lastFrameOnly, ModeImageToVideo},
		{"last frame without model support", false, video.GenerationRequest{ImageReference: "gs://bucket/kf.png", LastFrameReference: "gs://bucket/next.png"}, none, ModeImageToVideo},
	}
	for _, tt := range tests {
		if got := ClassifyRequest(tt.req, tt.usePreviousVideo, tt.caps); got != tt.want {
			t.Errorf("%s: ClassifyRequest() = %s, want %s", tt.name, got, tt.want)
		}
	}
}

type capsFakeRunner struct {
	refs      bool
	lastFrame bool
}

func (r capsFakeRunner) Run(context.Context, video.GenerationRequest) (*video.Response, error) {
	return nil, nil
}
func (r capsFakeRunner) SupportsReferenceImages() bool { return r.refs }
func (r capsFakeRunner) SupportsLastFrame() bool       { return r.lastFrame }

type plainFakeRunner struct{}

func (plainFakeRunner) Run(context.Context, video.GenerationRequest) (*video.Response, error) {
	return nil, nil
}

// TestRunnerCapabilities verifies capability derivation from a runner's optional interfaces,
// and that runners without them (test doubles) report no optional support.
func TestRunnerCapabilities(t *testing.T) {
	got := RunnerCapabilities(capsFakeRunner{refs: true, lastFrame: false})
	if !got.ReferenceImages || got.LastFrame {
		t.Errorf("RunnerCapabilities(refs only) = %+v", got)
	}
	got = RunnerCapabilities(capsFakeRunner{refs: false, lastFrame: true})
	if got.ReferenceImages || !got.LastFrame {
		t.Errorf("RunnerCapabilities(lastFrame only) = %+v", got)
	}
	got = RunnerCapabilities(plainFakeRunner{})
	if got.ReferenceImages || got.LastFrame {
		t.Errorf("RunnerCapabilities(plain) = %+v, want none", got)
	}
}

func TestModelCapabilities(t *testing.T) {
	tests := []struct {
		model string
		want  Capabilities
	}{
		{"veo-3.0-generate-001", Capabilities{ReferenceImages: true, LastFrame: false}},
		{"veo-3.1-generate-001", Capabilities{ReferenceImages: true, LastFrame: true}},
		{"veo-3.1-fast-generate-001", Capabilities{ReferenceImages: false, LastFrame: true}},
		{"veo-2.0-generate-001", Capabilities{ReferenceImages: false, LastFrame: true}},
		{"", Capabilities{}},
		{"other-model", Capabilities{}},
	}
	for _, tt := range tests {
		if got := ModelCapabilities(tt.model); got != tt.want {
			t.Errorf("ModelCapabilities(%q) = %+v, want %+v", tt.model, got, tt.want)
		}
	}
}
