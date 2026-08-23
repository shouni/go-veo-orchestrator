package runner

import (
	"context"
	"errors"
	"testing"

	"github.com/shouni/go-veo-orchestrator/video"
)

// failingVideoRunner は指定したカット以降の生成を失敗させる VideoRunner です。
type failingVideoRunner struct {
	mockVideoRunner
	failFromCutIndex int
	failErr          error
}

func (r *failingVideoRunner) Run(ctx context.Context, req video.GenerationRequest) (*video.Response, error) {
	if req.CutIndex >= r.failFromCutIndex {
		return nil, r.failErr
	}
	return r.mockVideoRunner.Run(ctx, req)
}

func threeCutRecipe() *video.Recipe {
	return &video.Recipe{
		ProjectTitle: "partial",
		Cuts: []video.Cut{
			{CutIndex: 1, AudioSync: video.AudioSync{DurationSec: 8}, KeyframeResult: video.KeyframeResult{KeyframeReference: "gs://images/cut_1.png"}, ChainControl: video.ChainControl{IsChainStart: true}},
			{CutIndex: 2, AudioSync: video.AudioSync{DurationSec: 8}, KeyframeResult: video.KeyframeResult{KeyframeReference: "gs://images/cut_2.png"}, ChainControl: video.ChainControl{IsChainStart: true}},
			{CutIndex: 3, AudioSync: video.AudioSync{DurationSec: 8}, KeyframeResult: video.KeyframeResult{KeyframeReference: "gs://images/cut_3.png"}, ChainControl: video.ChainControl{IsChainStart: true}},
		},
	}
}

// TestVideoTimelineRunner_RunReturnsPartialResultsOnFailure は、途中のカットで失敗しても
// 完了済みカットのレスポンスが返り、レシピにも生成結果が反映されていることを検証します。
// 1カットの生成には分単位の時間と実費が掛かるため、all-or-nothing では呼び出し側が
// flagship runner を使えず自前ループを書くことになります（実際に書いていました）。
func TestVideoTimelineRunner_RunReturnsPartialResultsOnFailure(t *testing.T) {
	boom := errors.New("veo exploded")
	videoRunner := &failingVideoRunner{failFromCutIndex: 3, failErr: boom}
	runner := NewVideoTimelineRunner(videoRunner)

	recipe := threeCutRecipe()
	responses, err := runner.Run(context.Background(), recipe)

	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want the generation failure", err)
	}
	if len(responses) != 2 {
		t.Fatalf("partial responses = %d, want 2 completed cuts", len(responses))
	}
	if recipe.Cuts[0].Status != video.CutStatusGenerated || recipe.Cuts[1].Status != video.CutStatusGenerated {
		t.Error("completed cuts must be marked generated for resume")
	}
	if recipe.Cuts[2].Status != video.CutStatusFailed {
		t.Errorf("failed cut status = %q, want failed", recipe.Cuts[2].Status)
	}
}

// TestVideoTimelineRunner_ObserverStopsRun は、CutObserver がエラーを返すと Run が
// そこで停止し部分結果を返すことを検証します（時間制限のあるジョブ基盤の「一旦保存して
// 次の実行で再開」に使う経路）。
func TestVideoTimelineRunner_ObserverStopsRun(t *testing.T) {
	stop := errors.New("deadline approaching")
	var observed []int
	runner := NewVideoTimelineRunner(&mockVideoRunner{}).
		WithCutObserver(func(_ context.Context, cut *video.Cut, _ *video.Response) error {
			observed = append(observed, cut.CutIndex)
			if len(observed) == 2 {
				return stop
			}
			return nil
		})

	recipe := threeCutRecipe()
	responses, err := runner.Run(context.Background(), recipe)

	if !errors.Is(err, stop) {
		t.Fatalf("error = %v, want the observer stop error", err)
	}
	if len(responses) != 2 {
		t.Fatalf("partial responses = %d, want 2", len(responses))
	}
	if len(observed) != 2 || observed[0] != 1 || observed[1] != 2 {
		t.Errorf("observed cuts = %v, want [1 2]", observed)
	}
	// observer で止まったカットは生成自体は成功している。
	if recipe.Cuts[1].Status != video.CutStatusGenerated {
		t.Errorf("cut 2 status = %q, want generated", recipe.Cuts[1].Status)
	}
}

// partialCutImageGenerator は2枚目で失敗するが1枚目の結果は返す CutImageGenerator です。
type partialCutImageGenerator struct{ err error }

// GenerateCut succeeds for the first cut and fails for every one after it, so the test can
// check that the image already produced stays saved when a later cut blows up.
func (g *partialCutImageGenerator) GenerateCut(_ context.Context, _ video.Cut, index, _ int) (*video.KeyframeImage, error) {
	if index == 1 {
		return &video.KeyframeImage{Data: []byte("img-1"), MimeType: "image/png", UsedSeed: 4242}, nil
	}
	return nil, g.err
}

// TestCutKeyframeRunner_GenerateAndSavePersistsPartialResults は、キーフレーム生成が途中で
// 失敗しても、成功した分の画像とメタデータが保存されてからエラーが返ることを検証します。
// 支払い済みの成果物を保存しておけば、再実行は失敗したカットだけを続きから生成できます。
func TestCutKeyframeRunner_GenerateAndSavePersistsPartialResults(t *testing.T) {
	boom := errors.New("image quota exceeded")
	writer := newFakeWriter()
	r := NewCutKeyframeRunner(&partialCutImageGenerator{err: boom}, writer)

	recipe := &video.Recipe{
		ProjectTitle: "partial keyframes",
		Cuts: []video.Cut{
			{CutIndex: 1, AudioSync: video.AudioSync{DurationSec: 8}},
			{CutIndex: 2, AudioSync: video.AudioSync{DurationSec: 8}},
		},
	}

	got, err := r.GenerateAndSave(context.Background(), recipe, "gs://bucket/jobs/j1/")
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want the generation failure", err)
	}
	if got == nil {
		t.Fatal("recipe must be returned with partial progress")
	}
	if got.Cuts[0].KeyframeReference == "" {
		t.Error("succeeded keyframe must be saved and referenced")
	}
	if got.Cuts[1].KeyframeReference != "" {
		t.Errorf("failed cut must stay pending, got %q", got.Cuts[1].KeyframeReference)
	}
	// 1枚のキーフレーム + メタデータの2書き込み。
	if writer.writeCount() != 2 {
		t.Fatalf("writes = %d, want keyframe + metadata", writer.writeCount())
	}
}
