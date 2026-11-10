package runner

import (
	"context"
	"errors"
	"testing"

	"github.com/shouni/go-veo-orchestrator/video"
)

// failingVideoRunner is a VideoRunner that fails generation from the given cut onward.
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

// TestVideoTimelineRunner_RunReturnsPartialResultsOnFailure verifies that even when
// a cut midway fails, the responses of the completed cuts are returned and the
// generation results are reflected in the recipe. Generating one cut takes minutes
// and real money, so with all-or-nothing callers could not use the flagship runner
// and would write their own loop (which they actually did).
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

// TestVideoTimelineRunner_ObserverStopsRun verifies that when a CutObserver returns
// an error, Run stops there and returns the partial result (the path used for "save
// for now and resume on the next run" on a job platform with a time limit).
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
	// The cut where the observer stopped did itself generate successfully.
	if recipe.Cuts[1].Status != video.CutStatusGenerated {
		t.Errorf("cut 2 status = %q, want generated", recipe.Cuts[1].Status)
	}
}

// TestVideoTimelineRunner_ObserverSkipsAlreadyGeneratedCuts verifies that resuming
// does not rerun post-processing for past cuts.
//
// The observer is used for work tied to generation finishing (recording charges,
// processing the outputs), so for a caller that resumes with one invocation per
// cut, every rerun would count the completed cuts twice.
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
	// Generated cuts are listed in the responses too (the caller expects to receive every cut).
	if len(responses) != 3 {
		t.Fatalf("responses = %d, want 3", len(responses))
	}
	if len(observed) != 2 || observed[0] != 2 || observed[1] != 3 {
		t.Errorf("observed cuts = %v, want [2 3] (cut 1 was already generated)", observed)
	}
}

// TestVideoTimelineRunner_CutGateSkipsAndBreaksTheChain verifies that a cut for which
// CutGate returns false is not generated, and that the chain does not connect across
// that gap.
func TestVideoTimelineRunner_CutGateSkipsAndBreaksTheChain(t *testing.T) {
	recipe := threeCutRecipe()
	// Take only the second cut out of scope (the way section-scoped generation skips out-of-scope cuts).
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
	// The far side of the gap must not connect to the video beyond it.
	if got := videoRunner.requests[1].PreviousVideoURI; got != "" {
		t.Errorf("cut 3 followed a skipped cut but carried PreviousVideoURI %q", got)
	}
}

// TestVideoTimelineRunner_CutGateSkipsGeneratedCuts verifies that the gate is not
// called for generated cuts. If it were called and could return false, an existing
// video would drop out of the chain.
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

// partialCutImageGenerator is a CutImageGenerator that fails on the second image but returns the first one's result.
type partialCutImageGenerator struct{ err error }

// GenerateCut succeeds for the first cut and fails for every one after it, so the test can
// check that the image already produced stays saved when a later cut blows up.
func (g *partialCutImageGenerator) GenerateCut(_ context.Context, _ video.Cut, index, _ int) (*video.KeyframeImage, error) {
	if index == 1 {
		return &video.KeyframeImage{Data: []byte("img-1"), MimeType: "image/png", UsedSeed: 4242}, nil
	}
	return nil, g.err
}

// TestCutKeyframeRunner_GenerateAndSavePersistsPartialResults verifies that even
// when keyframe generation fails midway, the images and metadata that succeeded are
// saved before the error is returned. With the paid-for outputs saved, a rerun can
// generate just the failed cuts, picking up where it stopped.
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
	// Two writes: one keyframe plus the metadata.
	if writer.writeCount() != 2 {
		t.Fatalf("writes = %d, want keyframe + metadata", writer.writeCount())
	}
}

// TestVideoTimelineRunner_MarksAChainStartAfterASkippedCut verifies that the far side
// of a skipped cut is recorded as a chain starting point.
//
// Without the mark, code that counts chain boundaries by "whether the next cut is a
// starting point" cannot see the previous chain's last cut, and it drops out of the
// join (that part vanishes entirely from the finished video).
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
