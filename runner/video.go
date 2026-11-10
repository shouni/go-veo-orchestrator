package runner

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/shouni/go-veo-orchestrator/ports"
	"github.com/shouni/go-veo-orchestrator/veo"
	"github.com/shouni/go-veo-orchestrator/video"
)

// CutObserver is a hook called each time one cut's video generation completes.
//
// It exists for resumable execution: the caller records per-cut progress here
// (saving metadata, checking an execution-time budget, etc.), and if it returns
// an error, Run stops there and returns the partial result so far. On a job
// platform with a time limit, "the deadline is near, so save for now and resume
// on the next run" works without replacing Run with a loop of one's own.
type CutObserver func(ctx context.Context, cut *video.Cut, res *video.Response) error

// CutGate is a hook called right before one cut's generation starts.
//
// Returning false skips the cut without generating it. A skipped cut has no
// video, so the chain hand-over breaks there too (the cuts after it do not
// connect across the gap to the earlier video). When the cut is generated, the
// passed recipe's cut can be rewritten here. Pre-generation adjustments, such as
// replacing the reference image at a chain start with the previous chain's last
// frame, belong here.
//
// It is not called for cuts already generated. Something that already has a video
// leaves no room for a generate-or-not decision, and skipping it would drop that
// video from the chain.
type CutGate func(ctx context.Context, recipe *video.Recipe, cutIndex int) (generate bool, err error)

// VideoTimelineRunner feeds keyframe results to Veo one after another, carrying the Video-to-Video context over.
type VideoTimelineRunner struct {
	videoRunner    ports.VideoRunner
	requestBuilder VideoRequestBuilder
	observer       CutObserver
	gate           CutGate
}

// NewVideoTimelineRunner initializes the video generation orchestrator.
func NewVideoTimelineRunner(
	videoRunner ports.VideoRunner,
) *VideoTimelineRunner {
	return &VideoTimelineRunner{
		videoRunner:    videoRunner,
		requestBuilder: NewVideoRequestBuilder(),
	}
}

// WithRequestBuilder replaces how video generation requests are assembled.
// Passing nil changes nothing; it returns the receiver to allow method chaining.
func (r *VideoTimelineRunner) WithRequestBuilder(builder VideoRequestBuilder) *VideoTimelineRunner {
	if builder != nil {
		r.requestBuilder = builder
	}
	return r
}

// WithCutObserver sets the hook called after each cut completes.
// Passing nil changes nothing; it returns the receiver to allow method chaining.
func (r *VideoTimelineRunner) WithCutObserver(observer CutObserver) *VideoTimelineRunner {
	if observer != nil {
		r.observer = observer
	}
	return r
}

// WithCutGate sets the hook called before each cut is generated.
// Passing nil changes nothing; it returns the receiver to allow method chaining.
func (r *VideoTimelineRunner) WithCutGate(gate CutGate) *VideoTimelineRunner {
	if gate != nil {
		r.gate = gate
	}
	return r
}

// Run turns the cuts into video one after another from their keyframes, carrying
// the previous cut's VideoID over.
//
// Even on error, it returns the responses of the cuts completed so far as a partial
// result. Generating one cut takes minutes and real money, so even after a failure
// midway the caller can use the completed part to save and resume (the generation
// results are already reflected in the recipe's Cuts).
func (r *VideoTimelineRunner) Run(ctx context.Context, recipe *video.Recipe) ([]*video.Response, error) {
	if err := r.validateRun(recipe); err != nil {
		return nil, err
	}
	recipe.Normalize()

	warnMissingKeyframes(ctx, recipe)

	// The optional Veo features supported by the VideoRunner in use are the same
	// for every cut, so derive them once outside the loop.
	caps := veo.RunnerCapabilities(r.videoRunner)

	responses := make([]*video.Response, 0, len(recipe.Cuts))
	lastVideoID := ""

	for i := range recipe.Cuts {
		// Note whether the cut was already generated before generating it. Once
		// runCut generates this cut, IsGenerated() turns true, so looking afterwards
		// cannot tell the difference.
		alreadyGenerated := recipe.Cuts[i].IsGenerated()

		if !alreadyGenerated && r.gate != nil {
			generate, err := r.gate(ctx, recipe, i)
			if err != nil {
				return responses, fmt.Errorf("cut %d: stopped at the generation gate: %w", recipe.Cuts[i].CutIndex, err)
			}
			if !generate {
				// There is no video at the skipped cut's position, so the next cut to be
				// generated does not connect across this gap to the earlier video. Reset
				// the hand-over source to empty.
				lastVideoID = ""
				continue
			}
		}

		res, err := r.runCut(ctx, recipe, i, lastVideoID, caps)
		if err != nil {
			return responses, err
		}
		responses = append(responses, res)
		lastVideoID = nextVideoID(lastVideoID, res)

		// Post-processing is not called for cuts already generated. The observer is
		// used for work tied to "generation finished" (recording charges, downstream
		// processing), so rerunning it for past cuts on every resume would count them
		// twice.
		if alreadyGenerated || r.observer == nil {
			continue
		}
		if err := r.observer(ctx, &recipe.Cuts[i], res); err != nil {
			return responses, fmt.Errorf("cut %d: stopped in post-processing: %w", recipe.Cuts[i].CutIndex, err)
		}
	}

	return responses, nil
}

func (r *VideoTimelineRunner) validateRun(recipe *video.Recipe) error {
	if recipe == nil {
		return ports.ErrRecipeRequired
	}
	if r.videoRunner == nil {
		return ports.ErrVideoRunnerRequired
	}
	return nil
}

// warnMissingKeyframes warns about cuts that have no keyframe reference.
//
// This runner does not generate keyframes (generating and saving them is
// CutKeyframeRunner's job, run first by the caller). A cut without a reference is
// generated from the prompt alone, so rather than proceeding silently, a record is
// left.
func warnMissingKeyframes(ctx context.Context, recipe *video.Recipe) {
	for i := range recipe.Cuts {
		cut := &recipe.Cuts[i]
		if cut.IsGenerated() || cut.KeyframeReference != "" {
			continue
		}
		slog.WarnContext(ctx, "Generating a cut without a keyframe reference from the prompt alone",
			"cut_index", cut.CutIndex)
	}
}

func (r *VideoTimelineRunner) runCut(
	ctx context.Context,
	recipe *video.Recipe,
	cutIndex int,
	lastVideoID string,
	caps veo.Capabilities,
) (*video.Response, error) {
	cut := &recipe.Cuts[cutIndex]
	if cut.IsGenerated() {
		return responseFromCut(*cut), nil
	}

	// A cut with IsChainStart is a fresh start of a video-to-video chain, so it
	// does not carry the previous cut's VideoID over as PreviousVideoURI; the chain
	// is reset here (a reset at a section boundary or on reaching the continuation
	// duration limit; see video.ChainControl).
	previousVideoURI := lastVideoID
	if cut.IsChainStart {
		previousVideoURI = ""
	}
	// A cut generated with no video to carry over is itself the start of a new
	// chain. Besides the starting points the planner set (already IsChainStart),
	// the far side of a gap left by a skipped cut lands here too. Proceeding
	// without marking it would hide the previous chain's last cut from code that
	// counts chain boundaries, and it would drop out of the join.
	if previousVideoURI == "" {
		cut.IsChainStart = true
	}

	req := r.requestBuilder.Build(BuildInput{
		Recipe:             recipe,
		Cut:                *cut,
		PreviousVideoURI:   previousVideoURI,
		LastFrameReference: video.Cuts(recipe.Cuts).NextLastFrameReference(cutIndex),
		Capabilities:       caps,
	})
	if err := validateCutDuration(req, caps); err != nil {
		cut.Status = video.CutStatusFailed
		return nil, err
	}

	res, err := r.videoRunner.Run(ctx, req)
	if err != nil {
		cut.Status = video.CutStatusFailed
		return nil, fmt.Errorf("cut %d: video generation failed: %w", cut.CutIndex, err)
	}
	if res == nil {
		// Treat a nil response as a failure too. Left pending, the resume side would
		// mistake it for "a cut not yet run".
		cut.Status = video.CutStatusFailed
		return nil, fmt.Errorf("cut %d: video generation response is nil", cut.CutIndex)
	}

	applyVideoResponse(cut.CutIndex, &cut.Result, res)
	return res, nil
}

// validateCutDuration checks that the duration of the request about to be sent to
// Veo is accepted by the generation mode the request resolves to (the allowed
// durations are veo.DurationsForMode).
//
// Sending it unchecked would also be rejected by Veo, but only after submitting
// and waiting on a long-running operation. Failing it here reports a
// duration-planning mistake on the recipe side — down to which cut, how many
// seconds and which mode — before the wait and the charges of cut generation.
func validateCutDuration(req video.GenerationRequest, caps veo.Capabilities) error {
	mode := veo.ClassifyRequest(req, req.PreviousVideoURI != "", caps)
	if veo.IsSupportedDuration(req.DurationSec, mode) {
		return nil
	}
	return fmt.Errorf("cut %d: %.2f seconds cannot be generated in %s (supported durations: %v): %w",
		req.CutIndex, req.DurationSec, mode, veo.DurationsForMode(mode), ports.ErrUnsupportedCutDuration)
}

func responseFromCut(cut video.Cut) *video.Response {
	return &video.Response{
		CloudURL:    cut.VideoURL,
		VideoID:     cut.VideoID,
		CutIndex:    cut.CutIndex,
		DurationSec: cut.DurationSec,
	}
}

// applyVideoResponse reflects the video generation result (res) into the cut's
// Result. The signature shows that apart from cutIndex, it reads and writes only
// the Result fields.
func applyVideoResponse(cutIndex int, result *video.Result, res *video.Response) {
	if res.CutIndex == 0 {
		res.CutIndex = cutIndex
	}
	result.VideoURL = res.CloudURL
	result.VideoID = res.VideoID
	result.Status = video.CutStatusGenerated
}

func nextVideoID(current string, res *video.Response) string {
	if res.VideoID == "" {
		return current
	}
	return res.VideoID
}
