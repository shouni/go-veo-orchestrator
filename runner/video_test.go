package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	"github.com/shouni/go-veo-orchestrator/ports"
)

type mockCutKeyframeRunner struct {
	images []*imagePorts.ImageResponse
	calls  int
}

func (m *mockCutKeyframeRunner) Run(_ context.Context, _ *ports.VideoRecipe) ([]*imagePorts.ImageResponse, error) {
	m.calls++
	return m.images, nil
}

func (m *mockCutKeyframeRunner) RunAndSave(_ context.Context, recipe *ports.VideoRecipe, _ string) (*ports.VideoRecipe, error) {
	return recipe, nil
}

func (m *mockCutKeyframeRunner) EditAndSave(_ context.Context, recipe *ports.VideoRecipe, _ string, _ string) (*ports.VideoRecipe, error) {
	return recipe, nil
}

type mockVideoRunner struct {
	requests []ports.VideoGenerationRequest
}

func (m *mockVideoRunner) Run(_ context.Context, req ports.VideoGenerationRequest) (*ports.VideoResponse, error) {
	m.requests = append(m.requests, req)
	return &ports.VideoResponse{
		CloudURL: fmt.Sprintf("gs://videos/cut_%d.mp4", req.CutIndex),
		VideoID:  fmt.Sprintf("video-%d", req.CutIndex),
		CutIndex: req.CutIndex,
	}, nil
}

// recordingPublishRunner implements ports.VideoPublishRunner and records how it was called.
type recordingPublishRunner struct {
	result *ports.PublishResult
	err    error
	calls  int
}

func (p *recordingPublishRunner) Run(_ context.Context, _ *ports.VideoRecipe, _ string) (*ports.PublishResult, error) {
	p.calls++
	if p.err != nil {
		return nil, p.err
	}
	return p.result, nil
}

func (p *recordingPublishRunner) BuildMetadata(_ *ports.VideoRecipe) ([]byte, error) {
	return nil, nil
}

// recordingRequestBuilder implements VideoRequestBuilder so tests can assert that
// WithRequestBuilder actually swaps the runner's builder.
type recordingRequestBuilder struct{}

func (recordingRequestBuilder) Build(_ *ports.VideoRecipe, _ ports.Cut, _ *imagePorts.ImageResponse, _ string) ports.VideoGenerationRequest {
	return ports.VideoGenerationRequest{}
}

func TestVideoTimelineRunner_RunAndSave(t *testing.T) {
	ctx := context.Background()
	baseRecipe := func() *ports.VideoRecipe {
		return &ports.VideoRecipe{Cuts: []ports.Cut{
			{CutIndex: 1, DurationSec: 5, VisualAnchor: "a", KeyframeReference: "gs://images/cut_1.png"},
		}}
	}

	t.Run("publishes metadata when a publisher is configured", func(t *testing.T) {
		publisher := &recordingPublishRunner{result: &ports.PublishResult{MetadataPath: "gs://out/meta.json"}}
		runner := NewVideoTimelineRunner(&mockCutKeyframeRunner{}, &mockVideoRunner{}, publisher)

		got, err := runner.RunAndSave(ctx, baseRecipe(), "gs://out/")
		if err != nil {
			t.Fatalf("RunAndSave() error = %v", err)
		}
		if got.Metadata == nil || got.Metadata.MetadataPath != "gs://out/meta.json" {
			t.Fatalf("Metadata = %+v", got.Metadata)
		}
		if publisher.calls != 1 {
			t.Fatalf("publisher calls = %d, want 1", publisher.calls)
		}
		if len(got.Videos) != 1 {
			t.Fatalf("videos = %d, want 1", len(got.Videos))
		}
	})

	t.Run("skips publishing when no publisher is configured", func(t *testing.T) {
		runner := NewVideoTimelineRunner(&mockCutKeyframeRunner{}, &mockVideoRunner{}, nil)

		got, err := runner.RunAndSave(ctx, baseRecipe(), "gs://out/")
		if err != nil {
			t.Fatalf("RunAndSave() error = %v", err)
		}
		if got.Metadata != nil {
			t.Fatalf("expected nil Metadata when no publisher is configured, got %+v", got.Metadata)
		}
	})

	t.Run("propagates a publisher failure", func(t *testing.T) {
		publisher := &recordingPublishRunner{err: fmt.Errorf("write failed")}
		runner := NewVideoTimelineRunner(&mockCutKeyframeRunner{}, &mockVideoRunner{}, publisher)

		if _, err := runner.RunAndSave(ctx, baseRecipe(), "gs://out/"); err == nil {
			t.Fatal("expected error when publisher fails")
		}
	})

	t.Run("propagates a Run failure before publishing is attempted", func(t *testing.T) {
		publisher := &recordingPublishRunner{}
		runner := NewVideoTimelineRunner(&mockCutKeyframeRunner{}, &mockVideoRunner{}, publisher)

		_, err := runner.RunAndSave(ctx, nil, "gs://out/")
		if !errors.Is(err, ports.ErrRecipeRequired) {
			t.Fatalf("expected ErrRecipeRequired, got %v", err)
		}
		if publisher.calls != 0 {
			t.Fatalf("expected publisher not to be called, calls = %d", publisher.calls)
		}
	})
}

func TestVideoTimelineRunner_WithRequestBuilder(t *testing.T) {
	t.Run("overrides the default request builder", func(t *testing.T) {
		custom := recordingRequestBuilder{}
		runner := NewVideoTimelineRunner(&mockCutKeyframeRunner{}, &mockVideoRunner{}, nil).WithRequestBuilder(custom)

		if runner.requestBuilder != custom {
			t.Fatal("expected custom builder to be set")
		}
	})

	t.Run("nil builder leaves the default in place", func(t *testing.T) {
		runner := NewVideoTimelineRunner(&mockCutKeyframeRunner{}, &mockVideoRunner{}, nil)
		original := runner.requestBuilder

		runner.WithRequestBuilder(nil)

		if runner.requestBuilder != original {
			t.Fatal("expected default builder to remain unchanged when passed nil")
		}
	})
}

func TestVideoTimelineRunner_RunChainsPreviousVideoID(t *testing.T) {
	ctx := context.Background()
	recipe := &ports.VideoRecipe{
		ProjectTitle: "test",
		MusicRecipe:  ports.MusicRecipe{Mood: "symphonic rock"},
		Cuts: []ports.Cut{
			{
				CutIndex:       1,
				DurationSec:    5,
				AudioCue:       "intro pad",
				VisualAnchor:   "slow dolly in",
				AudioReference: "gs://audio/seg_1.mp3",
			},
			{
				CutIndex:     2,
				DurationSec:  5,
				AudioCue:     "heavy chorus",
				VisualAnchor: "fast orbit camera",
			},
		},
	}
	keyframes := &mockCutKeyframeRunner{
		images: []*imagePorts.ImageResponse{
			{Data: []byte("image-1"), MimeType: "image/png", UsedSeed: 101},
			{Data: []byte("image-2"), MimeType: "image/png", UsedSeed: 102},
		},
	}
	video := &mockVideoRunner{}
	runner := NewVideoTimelineRunner(keyframes, video, nil)

	res, err := runner.Run(ctx, recipe)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("Expected 2 video responses, got %d", len(res))
	}
	if len(video.requests) != 2 {
		t.Fatalf("Expected 2 video requests, got %d", len(video.requests))
	}
	if video.requests[0].PreviousVideoID != "" {
		t.Errorf("Expected first request to have no previous video ID, got %q", video.requests[0].PreviousVideoID)
	}
	if video.requests[1].PreviousVideoID != "video-1" {
		t.Errorf("Expected second request to chain video-1, got %q", video.requests[1].PreviousVideoID)
	}
	if string(video.requests[0].InputImage) != "image-1" {
		t.Errorf("Expected first input image data, got %q", string(video.requests[0].InputImage))
	}
	if video.requests[0].ImageReference != "" {
		t.Errorf("Expected first image reference, got %q", video.requests[0].ImageReference)
	}
	if video.requests[0].AudioReference != "gs://audio/seg_1.mp3" {
		t.Errorf("Expected first audio reference, got %q", video.requests[0].AudioReference)
	}
	if video.requests[0].Seed != 101 {
		t.Errorf("Expected seed from keyframe, got %d", video.requests[0].Seed)
	}
	if !strings.Contains(video.requests[1].Prompt, "heavy chorus") {
		t.Errorf("Expected audio cue in prompt, got %q", video.requests[1].Prompt)
	}
	if recipe.Cuts[1].VideoURL != "gs://videos/cut_2.mp4" {
		t.Errorf("Expected recipe to be updated with video URL, got %q", recipe.Cuts[1].VideoURL)
	}
	if recipe.Cuts[1].VideoID != "video-2" {
		t.Errorf("Expected recipe to be updated with video ID, got %q", recipe.Cuts[1].VideoID)
	}
}

func TestVideoTimelineRunner_RunUsesSavedKeyframeReferences(t *testing.T) {
	ctx := context.Background()
	recipe := &ports.VideoRecipe{
		ProjectTitle: "saved keyframes",
		Cuts: []ports.Cut{
			{
				CutIndex:          1,
				DurationSec:       5,
				VisualAnchor:      "first saved keyframe",
				KeyframeReference: "gs://images/cut_1.png",
			},
			{
				CutIndex:          2,
				DurationSec:       5,
				VisualAnchor:      "second saved keyframe",
				KeyframeReference: "gs://images/cut_2.png",
			},
		},
	}
	keyframes := &mockCutKeyframeRunner{}
	video := &mockVideoRunner{}
	runner := NewVideoTimelineRunner(keyframes, video, nil)

	if _, err := runner.Run(ctx, recipe); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if keyframes.calls != 0 {
		t.Fatalf("keyframe runner calls = %d, want 0", keyframes.calls)
	}
	if len(video.requests) != 2 {
		t.Fatalf("video requests = %d, want 2", len(video.requests))
	}
	if video.requests[0].ImageReference != "gs://images/cut_1.png" {
		t.Fatalf("first image reference = %q", video.requests[0].ImageReference)
	}
	if len(video.requests[0].InputImage) != 0 {
		t.Fatalf("first input image should be empty when image reference exists")
	}
}

func TestVideoTimelineRunner_RunSkipsGeneratedCutAndChainsItsVideoID(t *testing.T) {
	ctx := context.Background()
	recipe := &ports.VideoRecipe{
		ProjectTitle: "resume",
		Cuts: []ports.Cut{
			{
				CutIndex:     1,
				DurationSec:  5,
				VisualAnchor: "resume from existing context",
				VideoID:      "existing-video-1",
				VideoURL:     "gs://videos/cut_1.mp4",
				Status:       ports.CutStatusGenerated,
			},
			{
				CutIndex:     2,
				DurationSec:  5,
				VisualAnchor: "continue from existing context",
			},
		},
	}
	keyframes := &mockCutKeyframeRunner{
		images: []*imagePorts.ImageResponse{
			{Data: []byte("image-1"), MimeType: "image/png", UsedSeed: 101},
			{Data: []byte("image-2"), MimeType: "image/png", UsedSeed: 102},
		},
	}
	video := &mockVideoRunner{}
	runner := NewVideoTimelineRunner(keyframes, video, nil)

	_, err := runner.Run(ctx, recipe)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if len(video.requests) != 1 {
		t.Fatalf("Expected only pending cut to be requested, got %d", len(video.requests))
	}
	if video.requests[0].PreviousVideoID != "existing-video-1" {
		t.Errorf("Expected generated cut video ID as previous context, got %q", video.requests[0].PreviousVideoID)
	}
}
