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
			{CutIndex: 1, DurationSec: 8, KeyframeReference: "gs://images/cut_1.png", IsChainStart: true},
			{CutIndex: 2, DurationSec: 8, KeyframeReference: "gs://images/cut_2.png", IsChainStart: true},
			{CutIndex: 3, DurationSec: 8, KeyframeReference: "gs://images/cut_3.png", IsChainStart: true},
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

// TestVideoTimelineRunner_ObserverSkipsAlreadyGeneratedCuts は、再開時に過去のカットぶんの
// 後処理が走り直さないことを検証します。
//
// observer は生成が終わったことに紐づく処理（課金の記録、生成物の加工）に使われるので、
// 1カット1起動で再開する呼び出し側では、走り直すたびに完了済みカットが二重計上されます。
func TestVideoTimelineRunner_ObserverSkipsAlreadyGeneratedCuts(t *testing.T) {
	recipe := threeCutRecipe()
	recipe.Cuts[0].Status = video.CutStatusGenerated
	recipe.Cuts[0].VideoID = "gs://bucket/video-1.mp4"
	recipe.Cuts[0].VideoURL = "gs://videos/cut_1.mp4"

	var observed []int
	runner := NewVideoTimelineRunner(&mockVideoRunner{}).
		WithCutObserver(func(_ context.Context, cut *video.Cut, _ *video.Response) error {
			observed = append(observed, cut.CutIndex)
			return nil
		})

	responses, err := runner.Run(context.Background(), recipe)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// 生成済みカットもレスポンスには並ぶ（呼び出し側が全カット分を受け取る前提のため）。
	if len(responses) != 3 {
		t.Fatalf("responses = %d, want 3", len(responses))
	}
	if len(observed) != 2 || observed[0] != 2 || observed[1] != 3 {
		t.Errorf("observed cuts = %v, want [2 3] (cut 1 was already generated)", observed)
	}
}

// TestVideoTimelineRunner_CutGateSkipsAndBreaksTheChain は、CutGate が false を返したカットが
// 生成されず、かつその穴を跨いでチェーンが繋がらないことを検証します。
func TestVideoTimelineRunner_CutGateSkipsAndBreaksTheChain(t *testing.T) {
	recipe := threeCutRecipe()
	// 2 本目だけ担当外にする（セクション単位の生成が担当外カットを飛ばす形）。
	recipe.Cuts[1].IsChainStart = false
	recipe.Cuts[2].IsChainStart = false

	videoRunner := &mockVideoRunner{}
	var gated []int
	runner := NewVideoTimelineRunner(videoRunner).
		WithCutGate(func(_ context.Context, r *video.Recipe, i int) (bool, error) {
			gated = append(gated, r.Cuts[i].CutIndex)
			return r.Cuts[i].CutIndex != 2, nil
		})

	if _, err := runner.Run(context.Background(), recipe); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(gated) != 3 {
		t.Fatalf("gate calls = %v, want one per pending cut", gated)
	}
	if len(videoRunner.requests) != 2 {
		t.Fatalf("video requests = %d, want 2 (cut 2 was skipped)", len(videoRunner.requests))
	}
	if recipe.Cuts[1].IsGenerated() {
		t.Error("cut 2 was skipped by the gate but came back generated")
	}
	// 穴の向こう側は、跨いだ先の動画へ繋がってはいけない。
	if got := videoRunner.requests[1].PreviousVideoURI; got != "" {
		t.Errorf("cut 3 followed a skipped cut but carried PreviousVideoURI %q", got)
	}
}

// TestVideoTimelineRunner_CutGateSkipsGeneratedCuts は、生成済みカットに gate が呼ばれない
// ことを検証します。呼んで false を返せると、既にある動画がチェーンから落ちます。
func TestVideoTimelineRunner_CutGateSkipsGeneratedCuts(t *testing.T) {
	recipe := threeCutRecipe()
	recipe.Cuts[0].Status = video.CutStatusGenerated
	recipe.Cuts[0].VideoID = "gs://bucket/video-1.mp4"

	var gated []int
	runner := NewVideoTimelineRunner(&mockVideoRunner{}).
		WithCutGate(func(_ context.Context, r *video.Recipe, i int) (bool, error) {
			gated = append(gated, r.Cuts[i].CutIndex)
			return true, nil
		})

	if _, err := runner.Run(context.Background(), recipe); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(gated) != 2 || gated[0] != 2 {
		t.Errorf("gate calls = %v, want [2 3] (cut 1 was already generated)", gated)
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
			{CutIndex: 1, DurationSec: 8},
			{CutIndex: 2, DurationSec: 8},
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

// TestVideoTimelineRunner_MarksAChainStartAfterASkippedCut は、飛ばされたカットの向こう側が
// チェーンの起点として記録されることを検証します。
//
// 印が無いと、チェーンの境界を「次のカットが起点かどうか」で数える側から直前チェーンの
// 最終カットが見えず、結合の対象から落ちます（完成動画からそのぶんが丸ごと消えます）。
func TestVideoTimelineRunner_MarksAChainStartAfterASkippedCut(t *testing.T) {
	recipe := threeCutRecipe()
	for i := range recipe.Cuts {
		recipe.Cuts[i].IsChainStart = false
	}

	runner := NewVideoTimelineRunner(&mockVideoRunner{}).
		WithCutGate(func(_ context.Context, r *video.Recipe, i int) (bool, error) {
			return r.Cuts[i].CutIndex != 2, nil
		})
	if _, err := runner.Run(context.Background(), recipe); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if !recipe.Cuts[0].IsChainStart {
		t.Error("cut 1 opens the job with no previous video but was not marked a chain start")
	}
	if !recipe.Cuts[2].IsChainStart {
		t.Error("cut 3 follows a skipped cut but was not marked a chain start")
	}
}
