// Package veo handles the Veo API's constraints: classifying the generation mode,
// the durations each mode allows, and cut-duration planning. It generates no
// video itself; it only decides which mode a request is interpreted as and which
// durations that mode allows. The actual Veo call is the job of the
// ports.VideoRunner the caller injects.
package veo

import (
	"math"
	"slices"
)

// Constants for the cut durations (in seconds) Veo accepts. Veo cannot generate
// video of arbitrary length and accepts only discrete durations per generation
// mode, so planning, normalizing and validating cut durations is all based on
// these values.
const (
	// MaxCutDurationSec is the longest cut duration (seconds) image_to_video accepts.
	MaxCutDurationSec = 8.0

	// VideoExtensionDurationSec is the only cut duration (seconds) video_extension
	// accepts (video-to-video: generation that carries the previous cut's video
	// over as PreviousVideoID).
	VideoExtensionDurationSec = 7.0

	// ContinuationMaxDurationSec is the threshold for resetting to a new chain
	// just before one continuation chain's cumulative duration (seconds) reaches
	// this value.
	//
	// The API's upper limit on the cumulative duration video_extension accepts as
	// "the previous video" is 30 seconds (up to 29 seconds cumulative succeeds;
	// passing 36 seconds cumulative fails with "Video duration 36 seconds exceeds
	// the maximum duration 30 seconds" (code=3)). However, because video_extension
	// reuses the previous generation as a conditioning input, saturation and
	// contrast drift and accumulate with every continuation (confirmed on real
	// jobs: saturation +20% on the first continuation, and contrast kept rising
	// monotonically in the rounds after). So instead of the API limit of 30
	// seconds, chains are reset early at this lower value, which bounds the number
	// of continuations per chain, and with it the accumulated drift.
	ContinuationMaxDurationSec = 24.0
)

// imageToVideoDurations and the others are the ascending lists of durations
// (seconds) Veo accepts in each generation mode. Only functions returning copies
// are exported, so callers cannot rewrite them by mistake.
var (
	imageToVideoDurations     = []float64{4, 6, 8}
	referenceToVideoDurations = []float64{8}
	videoExtensionDurations   = []float64{VideoExtensionDurationSec}
)

// ImageToVideoDurationsSec returns the cut durations (seconds) image_to_video /
// frames_to_video accept, in ascending order.
func ImageToVideoDurationsSec() []float64 { return slices.Clone(imageToVideoDurations) }

// ReferenceToVideoDurationsSec returns the cut durations (seconds)
// reference_to_video (referenceImages) accepts. Unlike image_to_video's {4,6,8},
// it is fixed at 8 seconds.
func ReferenceToVideoDurationsSec() []float64 {
	return slices.Clone(referenceToVideoDurations)
}

// DurationsForMode returns the cut durations (seconds) Veo accepts in the given
// generation mode, in ascending order. frames_to_video involves image input, so it
// has the same set as image_to_video.
func DurationsForMode(mode GenerationMode) []float64 {
	switch mode {
	case ModeReferenceToVideo:
		return ReferenceToVideoDurationsSec()
	case ModeVideoExtension:
		return slices.Clone(videoExtensionDurations)
	case ModeImageToVideo, ModeFramesToVideo:
		return ImageToVideoDurationsSec()
	default:
		return ImageToVideoDurationsSec()
	}
}

// IsSupportedDuration reports whether Veo accepts durationSec in mode. Durations
// are discrete whole seconds, so the comparison tolerates floating-point rounding
// error.
func IsSupportedDuration(durationSec float64, mode GenerationMode) bool {
	for _, candidate := range DurationsForMode(mode) {
		if math.Abs(durationSec-candidate) < durationEpsilon {
			return true
		}
	}
	return false
}

// durationEpsilon is the tolerance used when comparing durations. Durations take
// only whole seconds, so it only needs to be wide enough to treat tiny deviations
// from error diffusion and the like as equal.
const durationEpsilon = 1e-6

// SnapDuration rounds the duration to the nearest of candidates (the longer one on
// a tie). When candidates is empty, it returns durationSec unchanged.
func SnapDuration(durationSec float64, candidates []float64) float64 {
	if len(candidates) == 0 {
		return durationSec
	}
	best := candidates[0]
	for _, candidate := range candidates {
		if math.Abs(durationSec-candidate) <= math.Abs(durationSec-best) {
			best = candidate
		}
	}
	return best
}

// ChainDurations returns, in ascending order, the candidate total durations
// (seconds) that can be generated as one continuation chain (one base cut plus
// a run of video_extension continuation cuts). bases are the durations allowed
// for the chain's first cut ({4,6,8} for image_to_video, {8} for
// reference_to_video).
//
// Each candidate is "base duration + VideoExtensionDurationSec × number of
// continuations", extended until just before the cumulative duration would
// exceed ContinuationMaxDurationSec. With the default image_to_video bases this
// is {4,6,8,11,13,15,18,20,22}. Callers planning durations per chain use these
// as the achievable total durations.
func ChainDurations(bases []float64) []float64 {
	seen := make(map[float64]bool, len(bases))
	out := make([]float64, 0, len(bases))
	for _, base := range bases {
		for d := base; ; d += VideoExtensionDurationSec {
			if !seen[d] {
				seen[d] = true
				out = append(out, d)
			}
			if d+VideoExtensionDurationSec > ContinuationMaxDurationSec {
				break
			}
		}
	}
	slices.Sort(out)
	return out
}
