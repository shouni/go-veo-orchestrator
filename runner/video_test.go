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

// recordingRequestBuilder implements VideoRequestBuilder so tests can assert that
// WithRequestBuilder actually swaps the runner's builder.
type recordingRequestBuilder struct{}

func (recordingRequestBuilder) Build(BuildInput) ports.VideoGenerationRequest {
	return ports.VideoGenerationRequest{}
}

func TestVideoTimelineRunner_WithRequestBuilder(t *testing.T) {
	t.Run("overrides the default request builder", func(t *testing.T) {
		custom := recordingRequestBuilder{}
		runner := NewVideoTimelineRunner(&mockCutKeyframeRunner{}, &mockVideoRunner{}).WithRequestBuilder(custom)

		if runner.requestBuilder != custom {
			t.Fatal("expected custom builder to be set")
		}
	})

	t.Run("nil builder leaves the default in place", func(t *testing.T) {
		runner := NewVideoTimelineRunner(&mockCutKeyframeRunner{}, &mockVideoRunner{})
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
				CutIndex:     1,
				VisualAnchor: "slow dolly in",
				AudioSync: ports.AudioSync{
					DurationSec:    8,
					AudioCue:       "intro pad",
					AudioReference: "gs://audio/seg_1.mp3",
				},
			},
			{
				CutIndex:     2,
				VisualAnchor: "fast orbit camera",
				AudioSync: ports.AudioSync{
					DurationSec: 8,
					AudioCue:    "heavy chorus",
				},
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
	runner := NewVideoTimelineRunner(keyframes, video)

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

func TestVideoTimelineRunner_RunResetsChainAtChainStart(t *testing.T) {
	ctx := context.Background()
	recipe := &ports.VideoRecipe{
		ProjectTitle: "chain reset",
		Cuts: []ports.Cut{
			{
				CutIndex:     1,
				VisualAnchor: "chain head",
				AudioSync:    ports.AudioSync{DurationSec: 8},
			},
			{
				CutIndex:     2,
				VisualAnchor: "continues from cut 1",
				AudioSync:    ports.AudioSync{DurationSec: 8},
			},
			{
				CutIndex:     3,
				VisualAnchor: "new chain start (section boundary)",
				AudioSync:    ports.AudioSync{DurationSec: 8},
				ChainControl: ports.ChainControl{IsChainStart: true},
			},
		},
	}
	keyframes := &mockCutKeyframeRunner{
		images: []*imagePorts.ImageResponse{
			{Data: []byte("image-1"), MimeType: "image/png", UsedSeed: 101},
			{Data: []byte("image-2"), MimeType: "image/png", UsedSeed: 102},
			{Data: []byte("image-3"), MimeType: "image/png", UsedSeed: 103},
		},
	}
	video := &mockVideoRunner{}
	runner := NewVideoTimelineRunner(keyframes, video)

	if _, err := runner.Run(ctx, recipe); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if len(video.requests) != 3 {
		t.Fatalf("Expected 3 video requests, got %d", len(video.requests))
	}
	if video.requests[0].PreviousVideoID != "" {
		t.Errorf("cut 1 (first cut) should have no previous video ID, got %q", video.requests[0].PreviousVideoID)
	}
	if video.requests[1].PreviousVideoID != "video-1" {
		t.Errorf("cut 2 (non-chain-start) should chain video-1, got %q", video.requests[1].PreviousVideoID)
	}
	if video.requests[2].PreviousVideoID != "" {
		t.Errorf("cut 3 (IsChainStart) should reset the chain, got %q", video.requests[2].PreviousVideoID)
	}
}

func TestVideoTimelineRunner_RunUsesSavedKeyframeReferences(t *testing.T) {
	ctx := context.Background()
	recipe := &ports.VideoRecipe{
		ProjectTitle: "saved keyframes",
		Cuts: []ports.Cut{
			{
				CutIndex:       1,
				VisualAnchor:   "first saved keyframe",
				AudioSync:      ports.AudioSync{DurationSec: 8},
				KeyframeResult: ports.KeyframeResult{KeyframeReference: "gs://images/cut_1.png"},
			},
			{
				CutIndex:       2,
				VisualAnchor:   "second saved keyframe",
				AudioSync:      ports.AudioSync{DurationSec: 8},
				KeyframeResult: ports.KeyframeResult{KeyframeReference: "gs://images/cut_2.png"},
			},
		},
	}
	keyframes := &mockCutKeyframeRunner{}
	video := &mockVideoRunner{}
	runner := NewVideoTimelineRunner(keyframes, video)

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
				VisualAnchor: "resume from existing context",
				AudioSync:    ports.AudioSync{DurationSec: 8},
				VideoResult: ports.VideoResult{
					VideoID:  "existing-video-1",
					VideoURL: "gs://videos/cut_1.mp4",
					Status:   ports.CutStatusGenerated,
				},
			},
			{
				CutIndex:     2,
				VisualAnchor: "continue from existing context",
				AudioSync:    ports.AudioSync{DurationSec: 8},
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
	runner := NewVideoTimelineRunner(keyframes, video)

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

// lastFrameVideoRunner は lastFrame（frames_to_video 補間）に対応したモデルを表す
// テストダブルです。ports.LastFrameSupporter の実装有無だけが mockVideoRunner との違いです。
type lastFrameVideoRunner struct {
	mockVideoRunner
}

func (*lastFrameVideoRunner) SupportsLastFrame() bool { return true }

// TestVideoTimelineRunner_RunPassesNextKeyframeAsLastFrame は、lastFrame 対応モデルの場合に
// 次カットのキーフレームが終了フレームとして渡り、セクション境界の手前では渡らないことを
// 検証します（意図的な場面転換の直前で次セクションの構図へ寄せないため）。
func TestVideoTimelineRunner_RunPassesNextKeyframeAsLastFrame(t *testing.T) {
	ctx := context.Background()
	recipe := &ports.VideoRecipe{
		ProjectTitle: "last frame",
		Cuts: []ports.Cut{
			{
				CutIndex:       1,
				VisualAnchor:   "a",
				CharacterID:    "zundamon",
				AudioSync:      ports.AudioSync{DurationSec: 8},
				KeyframeResult: ports.KeyframeResult{KeyframeReference: "gs://images/cut_1.png"},
			},
			{
				CutIndex:       2,
				VisualAnchor:   "b",
				CharacterID:    "zundamon",
				AudioSync:      ports.AudioSync{DurationSec: 8},
				KeyframeResult: ports.KeyframeResult{KeyframeReference: "gs://images/cut_2.png"},
			},
			{
				CutIndex:       3,
				VisualAnchor:   "c",
				CharacterID:    "zundamon",
				AudioSync:      ports.AudioSync{DurationSec: 8},
				KeyframeResult: ports.KeyframeResult{KeyframeReference: "gs://images/cut_3.png"},
				ChainControl:   ports.ChainControl{IsChainStart: true, IsSectionStart: true},
			},
		},
	}
	video := &lastFrameVideoRunner{}
	runner := NewVideoTimelineRunner(&mockCutKeyframeRunner{}, video)

	if _, err := runner.Run(ctx, recipe); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if got := video.requests[0].LastFrameReference; got != "gs://images/cut_2.png" {
		t.Errorf("cut 1 last frame = %q, want the next cut's keyframe", got)
	}
	if got := video.requests[1].LastFrameReference; got != "" {
		t.Errorf("cut 2 last frame = %q, want none before a section boundary", got)
	}
	if got := video.requests[2].LastFrameReference; got != "" {
		t.Errorf("cut 3 last frame = %q, want none for the final cut", got)
	}
}

// TestVideoTimelineRunner_RunRejectsUnsupportedDuration は、Veo が受け付けない尺のカットを
// API へ投げる前に落とすことを検証します。Veo 側で拒否されるまで待つと、長時間実行
// オペレーションの往復と、それまでのカットの課金を無駄にします。
func TestVideoTimelineRunner_RunRejectsUnsupportedDuration(t *testing.T) {
	ctx := context.Background()
	recipe := &ports.VideoRecipe{
		ProjectTitle: "bad duration",
		Cuts: []ports.Cut{
			{
				CutIndex:       1,
				VisualAnchor:   "a",
				AudioSync:      ports.AudioSync{DurationSec: 12},
				KeyframeResult: ports.KeyframeResult{KeyframeReference: "gs://images/cut_1.png"},
			},
		},
	}
	video := &mockVideoRunner{}
	runner := NewVideoTimelineRunner(&mockCutKeyframeRunner{}, video)

	_, err := runner.Run(ctx, recipe)
	if !errors.Is(err, ports.ErrUnsupportedCutDuration) {
		t.Fatalf("Run() error = %v, want ErrUnsupportedCutDuration", err)
	}
	if len(video.requests) != 0 {
		t.Fatalf("video runner was called %d times, want 0", len(video.requests))
	}
	if recipe.Cuts[0].Status != ports.CutStatusFailed {
		t.Errorf("cut status = %q, want failed", recipe.Cuts[0].Status)
	}
}
