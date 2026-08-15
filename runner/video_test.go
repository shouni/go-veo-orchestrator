package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/shouni/go-veo-orchestrator/ports"
	"github.com/shouni/go-veo-orchestrator/video"
)

type mockVideoRunner struct {
	requests []video.GenerationRequest
}

func (m *mockVideoRunner) Run(_ context.Context, req video.GenerationRequest) (*video.Response, error) {
	m.requests = append(m.requests, req)
	return &video.Response{
		CloudURL: fmt.Sprintf("gs://videos/cut_%d.mp4", req.CutIndex),
		VideoID:  fmt.Sprintf("gs://bucket/video-%d.mp4", req.CutIndex),
		CutIndex: req.CutIndex,
	}, nil
}

// recordingRequestBuilder implements VideoRequestBuilder so tests can assert that
// WithRequestBuilder actually swaps the runner's builder.
type recordingRequestBuilder struct{}

func (recordingRequestBuilder) Build(BuildInput) video.GenerationRequest {
	return video.GenerationRequest{}
}

func TestVideoTimelineRunner_WithRequestBuilder(t *testing.T) {
	t.Run("overrides the default request builder", func(t *testing.T) {
		custom := recordingRequestBuilder{}
		runner := NewVideoTimelineRunner(&mockVideoRunner{}).WithRequestBuilder(custom)

		if runner.requestBuilder != custom {
			t.Fatal("expected custom builder to be set")
		}
	})

	t.Run("nil builder leaves the default in place", func(t *testing.T) {
		runner := NewVideoTimelineRunner(&mockVideoRunner{})
		original := runner.requestBuilder

		runner.WithRequestBuilder(nil)

		if runner.requestBuilder != original {
			t.Fatal("expected default builder to remain unchanged when passed nil")
		}
	})
}

func TestVideoTimelineRunner_RunChainsPreviousVideoURI(t *testing.T) {
	ctx := context.Background()
	recipe := &video.Recipe{
		ProjectTitle: "test",
		MusicRecipe:  video.MusicRecipe{Mood: "symphonic rock"},
		Cuts: []video.Cut{
			{
				CutIndex:     1,
				VisualAnchor: "slow dolly in",
				AudioSync: video.AudioSync{
					DurationSec:    8,
					AudioCue:       "intro pad",
					AudioReference: "gs://audio/seg_1.mp3",
				},
				// キーフレーム生成時のシードはカットに記録され、動画生成へ引き継がれる。
				KeyframeResult: video.KeyframeResult{KeyframeSeed: 101},
			},
			{
				CutIndex:     2,
				VisualAnchor: "fast orbit camera",
				AudioSync: video.AudioSync{
					// gs:// チェーン文脈があるため video_extension（7秒固定）になる。
					DurationSec: 7,
					AudioCue:    "heavy chorus",
				},
			},
		},
	}
	videoRunner := &mockVideoRunner{}
	runner := NewVideoTimelineRunner(videoRunner)

	res, err := runner.Run(ctx, recipe)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("Expected 2 video responses, got %d", len(res))
	}
	if len(videoRunner.requests) != 2 {
		t.Fatalf("Expected 2 video requests, got %d", len(videoRunner.requests))
	}
	if videoRunner.requests[0].PreviousVideoURI != "" {
		t.Errorf("Expected first request to have no previous video ID, got %q", videoRunner.requests[0].PreviousVideoURI)
	}
	if videoRunner.requests[1].PreviousVideoURI != "gs://bucket/video-1.mp4" {
		t.Errorf("Expected second request to chain gs://bucket/video-1.mp4, got %q", videoRunner.requests[1].PreviousVideoURI)
	}
	if len(videoRunner.requests[0].InputImage) != 0 {
		t.Errorf("keyframes travel as gs:// references, not bytes; got %d bytes", len(videoRunner.requests[0].InputImage))
	}
	if videoRunner.requests[0].ImageReference != "" {
		t.Errorf("Expected first image reference, got %q", videoRunner.requests[0].ImageReference)
	}
	if videoRunner.requests[0].AudioReference != "gs://audio/seg_1.mp3" {
		t.Errorf("Expected first audio reference, got %q", videoRunner.requests[0].AudioReference)
	}
	if videoRunner.requests[0].Seed != 101 {
		t.Errorf("Expected the keyframe seed recorded on the cut, got %d", videoRunner.requests[0].Seed)
	}
	if !strings.Contains(videoRunner.requests[1].Prompt, "heavy chorus") {
		t.Errorf("Expected audio cue in prompt, got %q", videoRunner.requests[1].Prompt)
	}
	if recipe.Cuts[1].VideoURL != "gs://videos/cut_2.mp4" {
		t.Errorf("Expected recipe to be updated with video URL, got %q", recipe.Cuts[1].VideoURL)
	}
	if recipe.Cuts[1].VideoID != "gs://bucket/video-2.mp4" {
		t.Errorf("Expected recipe to be updated with video ID, got %q", recipe.Cuts[1].VideoID)
	}
}

func TestVideoTimelineRunner_RunResetsChainAtChainStart(t *testing.T) {
	ctx := context.Background()
	recipe := &video.Recipe{
		ProjectTitle: "chain reset",
		Cuts: []video.Cut{
			{
				CutIndex:     1,
				VisualAnchor: "chain head",
				AudioSync:    video.AudioSync{DurationSec: 8},
			},
			{
				CutIndex:     2,
				VisualAnchor: "continues from cut 1",
				// gs:// チェーン文脈があるため video_extension（7秒固定）になる。
				AudioSync: video.AudioSync{DurationSec: 7},
			},
			{
				CutIndex:     3,
				VisualAnchor: "new chain start (section boundary)",
				AudioSync:    video.AudioSync{DurationSec: 8},
				ChainControl: video.ChainControl{IsChainStart: true},
			},
		},
	}
	videoRunner := &mockVideoRunner{}
	runner := NewVideoTimelineRunner(videoRunner)

	if _, err := runner.Run(ctx, recipe); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if len(videoRunner.requests) != 3 {
		t.Fatalf("Expected 3 video requests, got %d", len(videoRunner.requests))
	}
	if videoRunner.requests[0].PreviousVideoURI != "" {
		t.Errorf("cut 1 (first cut) should have no previous video ID, got %q", videoRunner.requests[0].PreviousVideoURI)
	}
	if videoRunner.requests[1].PreviousVideoURI != "gs://bucket/video-1.mp4" {
		t.Errorf("cut 2 (non-chain-start) should chain video-1, got %q", videoRunner.requests[1].PreviousVideoURI)
	}
	if videoRunner.requests[2].PreviousVideoURI != "" {
		t.Errorf("cut 3 (IsChainStart) should reset the chain, got %q", videoRunner.requests[2].PreviousVideoURI)
	}
}

func TestVideoTimelineRunner_RunUsesSavedKeyframeReferences(t *testing.T) {
	ctx := context.Background()
	recipe := &video.Recipe{
		ProjectTitle: "saved keyframes",
		Cuts: []video.Cut{
			{
				CutIndex:       1,
				VisualAnchor:   "first saved keyframe",
				AudioSync:      video.AudioSync{DurationSec: 8},
				KeyframeResult: video.KeyframeResult{KeyframeReference: "gs://images/cut_1.png"},
			},
			{
				CutIndex:     2,
				VisualAnchor: "second saved keyframe",
				// gs:// チェーン文脈があるため video_extension（7秒固定）になる。
				AudioSync:      video.AudioSync{DurationSec: 7},
				KeyframeResult: video.KeyframeResult{KeyframeReference: "gs://images/cut_2.png"},
			},
		},
	}
	videoRunner := &mockVideoRunner{}
	runner := NewVideoTimelineRunner(videoRunner)

	if _, err := runner.Run(ctx, recipe); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if len(videoRunner.requests) != 2 {
		t.Fatalf("video requests = %d, want 2", len(videoRunner.requests))
	}
	if videoRunner.requests[0].ImageReference != "gs://images/cut_1.png" {
		t.Fatalf("first image reference = %q", videoRunner.requests[0].ImageReference)
	}
	if len(videoRunner.requests[0].InputImage) != 0 {
		t.Fatalf("first input image should be empty when image reference exists")
	}
}

func TestVideoTimelineRunner_RunSkipsGeneratedCutAndChainsItsVideoID(t *testing.T) {
	ctx := context.Background()
	recipe := &video.Recipe{
		ProjectTitle: "resume",
		Cuts: []video.Cut{
			{
				CutIndex:     1,
				VisualAnchor: "resume from existing context",
				AudioSync:    video.AudioSync{DurationSec: 8},
				Result: video.Result{
					VideoID:  "gs://bucket/existing-video-1.mp4",
					VideoURL: "gs://videos/cut_1.mp4",
					Status:   video.CutStatusGenerated,
				},
			},
			{
				CutIndex:     2,
				VisualAnchor: "continue from existing context",
				// gs:// のチェーン文脈があるため video_extension（7秒固定）になる。
				AudioSync: video.AudioSync{DurationSec: 7},
			},
		},
	}
	videoRunner := &mockVideoRunner{}
	runner := NewVideoTimelineRunner(videoRunner)

	_, err := runner.Run(ctx, recipe)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if len(videoRunner.requests) != 1 {
		t.Fatalf("Expected only pending cut to be requested, got %d", len(videoRunner.requests))
	}
	if videoRunner.requests[0].PreviousVideoURI != "gs://bucket/existing-video-1.mp4" {
		t.Errorf("Expected generated cut video ID as previous context, got %q", videoRunner.requests[0].PreviousVideoURI)
	}
}

// TestVideoTimelineRunner_RunRejectsUnsupportedExtensionDuration は、gs:// のチェーン文脈を
// 持つカット（video_extension、7秒固定）に 8 秒が計画されていた場合に、送信前に
// ErrUnsupportedCutDuration で拒否されることを検証します。これは本番のチェーン経路の
// 検証で、以前は video_extension 側の validateCutDuration を通すテストがありませんでした。
func TestVideoTimelineRunner_RunRejectsUnsupportedExtensionDuration(t *testing.T) {
	ctx := context.Background()
	recipe := &video.Recipe{
		ProjectTitle: "bad extension duration",
		Cuts: []video.Cut{
			{
				CutIndex:  1,
				AudioSync: video.AudioSync{DurationSec: 8},
				Result: video.Result{
					VideoID:  "gs://bucket/existing-video-1.mp4",
					VideoURL: "gs://videos/cut_1.mp4",
					Status:   video.CutStatusGenerated,
				},
			},
			{
				CutIndex:       2,
				AudioSync:      video.AudioSync{DurationSec: 8}, // video_extension は7秒固定なので拒否される
				KeyframeResult: video.KeyframeResult{KeyframeReference: "gs://images/cut_2.png"},
			},
		},
	}
	videoRunner := &mockVideoRunner{}
	runner := NewVideoTimelineRunner(videoRunner)

	_, err := runner.Run(ctx, recipe)
	if !errors.Is(err, ports.ErrUnsupportedCutDuration) {
		t.Fatalf("error = %v, want ErrUnsupportedCutDuration", err)
	}
	if len(videoRunner.requests) != 0 {
		t.Fatalf("Veo リクエストは送られないべきですが %d 件送られました", len(videoRunner.requests))
	}
	if recipe.Cuts[1].Status != video.CutStatusFailed {
		t.Errorf("cut status = %q, want failed", recipe.Cuts[1].Status)
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
	recipe := &video.Recipe{
		ProjectTitle: "last frame",
		Cuts: []video.Cut{
			{
				CutIndex:       1,
				VisualAnchor:   "a",
				CharacterID:    "zundamon",
				AudioSync:      video.AudioSync{DurationSec: 8},
				KeyframeResult: video.KeyframeResult{KeyframeReference: "gs://images/cut_1.png"},
			},
			{
				CutIndex:       2,
				VisualAnchor:   "b",
				CharacterID:    "zundamon",
				AudioSync:      video.AudioSync{DurationSec: 8},
				KeyframeResult: video.KeyframeResult{KeyframeReference: "gs://images/cut_2.png"},
				// チェーン文脈を持ち込まない（lastFrame は image 入力とセットのときだけ有効）。
				ChainControl: video.ChainControl{IsChainStart: true},
			},
			{
				CutIndex:       3,
				VisualAnchor:   "c",
				CharacterID:    "zundamon",
				AudioSync:      video.AudioSync{DurationSec: 8},
				KeyframeResult: video.KeyframeResult{KeyframeReference: "gs://images/cut_3.png"},
				ChainControl:   video.ChainControl{IsChainStart: true, IsSectionStart: true},
			},
		},
	}
	videoRunner := &lastFrameVideoRunner{}
	runner := NewVideoTimelineRunner(videoRunner)

	if _, err := runner.Run(ctx, recipe); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if got := videoRunner.requests[0].LastFrameReference; got != "gs://images/cut_2.png" {
		t.Errorf("cut 1 last frame = %q, want the next cut's keyframe", got)
	}
	if got := videoRunner.requests[1].LastFrameReference; got != "" {
		t.Errorf("cut 2 last frame = %q, want none before a section boundary", got)
	}
	if got := videoRunner.requests[2].LastFrameReference; got != "" {
		t.Errorf("cut 3 last frame = %q, want none for the final cut", got)
	}
}

// TestVideoTimelineRunner_RunRejectsUnsupportedDuration は、Veo が受け付けない尺のカットを
// API へ投げる前に落とすことを検証します。Veo 側で拒否されるまで待つと、長時間実行
// オペレーションの往復と、それまでのカットの課金を無駄にします。
func TestVideoTimelineRunner_RunRejectsUnsupportedDuration(t *testing.T) {
	ctx := context.Background()
	recipe := &video.Recipe{
		ProjectTitle: "bad duration",
		Cuts: []video.Cut{
			{
				CutIndex:       1,
				VisualAnchor:   "a",
				AudioSync:      video.AudioSync{DurationSec: 12},
				KeyframeResult: video.KeyframeResult{KeyframeReference: "gs://images/cut_1.png"},
			},
		},
	}
	videoRunner := &mockVideoRunner{}
	runner := NewVideoTimelineRunner(videoRunner)

	_, err := runner.Run(ctx, recipe)
	if !errors.Is(err, ports.ErrUnsupportedCutDuration) {
		t.Fatalf("Run() error = %v, want ErrUnsupportedCutDuration", err)
	}
	if len(videoRunner.requests) != 0 {
		t.Fatalf("video runner was called %d times, want 0", len(videoRunner.requests))
	}
	if recipe.Cuts[0].Status != video.CutStatusFailed {
		t.Errorf("cut status = %q, want failed", recipe.Cuts[0].Status)
	}
}
