package veo

import (
	"math"
	"strings"

	characterkit "github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-veo-orchestrator/video"
)

// This file is the cut-duration planner. It provides pure functions that
// normalize a recipe's sequence of cuts of arbitrary duration into a sequence of
// cuts with the discrete durations Veo actually accepts (duration.go).
//
// Both the duration rules and the planning that uses them live in this package.
// Leaving consumers to write the planner would split rules and planning across
// repositories, and they would drift (which is what actually happened).

// ExpandCutsToSupportedDurations normalizes each cut's duration to a value Veo
// supports. A cut longer than 8 seconds is split into a run of sub-cuts that carry
// over the same keyframe and prompt, and the lyrics (Dialogue) are distributed
// evenly across the sub-cuts line by line. After splitting, CutIndex is renumbered
// from 1.
//
// When usePreviousVideo is true, the cuts after the first (those expected to be
// generated as video_extension with a PreviousVideoURI) are aligned to a fixed 7
// seconds. The chain is reset just before the cumulative duration reaches
// ContinuationMaxDurationSec, however, and a reset cut is treated as a new base
// that does not use PreviousVideoURI (image_to_video, {4,6,8} seconds), mirroring
// the lastVideoID reset on the execution side. Generated cuts are left unchanged so
// their metadata does not drift from the actual video's duration, but they are
// included in the cumulative-duration calculation so a resumed run carries the
// chain state over correctly.
//
// At a boundary where the song's section changes, the chain is reset even if there
// is room left in the cumulative duration. Section membership is decided from
// cut.SectionIndex (filled in from StartSec by VideoRecipe.Normalize). The
// difference from a technical reset is marked by IsSectionStart, and on cuts where
// this flag is set the execution side skips carrying over the previous chain's
// last frame (so the picture is not pulled toward the next section's composition
// right before an intended scene change).
//
// Cuts judged to be a chain's starting point are returned with IsChainStart set.
// This function is the only place that decides whether a cut is a starting point,
// so the decider records it. Readers reverse-deriving it from durations
// (isChainBase) is a fallback for old recipes, and an execution side that looks
// only at IsChainStart cannot recognize starting points that way.
//
// characters / referenceImagesSupported are used to decide each cut's allowed
// durations (see AllowedCutDurations). For how pre-planned chain blocks (ungenerated
// cuts with IsChainStart) are handled, see splitChainCutIntoSupportedDurations.
// Ungenerated cuts without IsChainStart (old recipes, etc.) are handled by greedy
// splitting and chain formation based on the cumulative duration.
func ExpandCutsToSupportedDurations(cuts []video.Cut, usePreviousVideo bool, characters *characterkit.Characters, referenceImagesSupported bool) []video.Cut {
	expanded := make([]video.Cut, 0, len(cuts))
	for _, cut := range cuts {
		var subCuts []video.Cut
		if usePreviousVideo && cut.IsChainStart && !cut.IsGenerated() {
			subCuts = splitChainCutIntoSupportedDurations(cut, AllowedCutDurations(cut, characters, referenceImagesSupported))
		} else {
			subCuts = SplitCutBySupportedDurations(cut, AllowedCutDurations(cut, characters, referenceImagesSupported))
		}
		expanded = append(expanded, subCuts...)
	}
	cumulative := 0.0
	for i := range expanded {
		expanded[i].CutIndex = i + 1
		if !usePreviousVideo {
			continue
		}
		if expanded[i].IsGenerated() {
			// If a generated cut was itself a chain's starting point (IsChainStart: the
			// first cut, or a past reset), the cumulative duration is counted again from
			// that cut's own duration. Always just adding would keep dragging the
			// cumulative duration of the chain before a reset along after it, and every
			// later cut would wrongly be treated as a reset (even though there is room
			// left in the cumulative duration). Because the execution side resumes one cut
			// at a time, this function recomputes every cut each time.
			if i == 0 || expanded[i].IsChainStart {
				cumulative = expanded[i].DurationSec
			} else {
				cumulative += expanded[i].DurationSec
			}
			continue
		}
		// IsSectionStart (cut.ChainControl) is a flag with overlapping meanings,
		// set for two different reasons. Here the two are decided separately and
		// then combined, so the code shows which reason caused the reset.
		//
		//   isSceneReset: the value the scene splitter set before this function. It
		//   is a "reset within a scene" from splitting one music section into several
		//   scenes, and SectionIndex does not change across it (so a SectionIndex
		//   comparison cannot detect it).
		//
		//   isRealSectionBoundary: an actual section boundary of the song, where
		//   cut.SectionIndex (1-based, 0 unassigned) differs from the previous cut.
		//   The pre-split cut's SectionIndex is copied to its sub-cuts (sub := cut in
		//   buildSubCutsFromDurations), so a split by itself is not treated as a
		//   section boundary.
		//
		// Both need the same downstream behavior, "do not carry over the previous
		// chain's last frame", so they are finally combined and written back into
		// the same IsSectionStart.
		isSceneReset := expanded[i].IsSectionStart
		isRealSectionBoundary := i > 0 && expanded[i].SectionIndex != 0 && expanded[i].SectionIndex != expanded[i-1].SectionIndex
		isSectionStart := isSceneReset || isRealSectionBoundary
		if isSectionStart {
			cumulative = 0
		}
		if expanded[i].IsChainStart || cumulative == 0 || cumulative+VideoExtensionDurationSec > ContinuationMaxDurationSec {
			// The head of a new chain (start of the song, a section boundary, right
			// after a reset, or the starting point of a pre-planned chain block). Use
			// the duration already assigned during splitting as-is ({4,6,8} seconds for
			// image_to_video, fixed at 8 seconds for reference_to_video).
			cumulative = expanded[i].DurationSec
			// Write the decision back into the cut. Without recording it here,
			// downstream can only reverse-derive the chain's starting point from
			// durations (the same trick as isChainBase). That reverse derivation is a
			// fallback for old recipes, and an execution side that looks only at
			// IsChainStart (VideoTimelineRunner) would recognize no starting point at
			// all and keep chaining to the very end without ever cutting the chain.
			expanded[i].IsChainStart = true
			if isSectionStart {
				expanded[i].IsSectionStart = true
			}
			continue
		}
		expanded[i].DurationSec = VideoExtensionDurationSec
		expanded[i].EndSec = expanded[i].StartSec + VideoExtensionDurationSec
		cumulative += VideoExtensionDurationSec
	}
	return expanded
}

// AllowedCutDurations returns the candidate durations (seconds) Veo accepts for the
// given cut, depending on whether it is generated by reference_to_video
// (referenceImages) or image_to_video (when referenceImagesSupported is false, the
// model does not support referenceImages, so image_to_video's {4,6,8} is returned).
func AllowedCutDurations(cut video.Cut, characters *characterkit.Characters, referenceImagesSupported bool) []float64 {
	if CutUsesReferenceImages(cut, characters, referenceImagesSupported) {
		return ReferenceToVideoDurationsSec()
	}
	return ImageToVideoDurationsSec()
}

// CutUsesReferenceImages reports whether this cut is generated with Veo's
// referenceImages (reference_to_video). The request is classified (ClassifyRequest)
// with the same reference image list the actual generation builds
// (CutReferenceImages), so the decision used by duration normalization and by the
// actual generation agree by construction.
func CutUsesReferenceImages(cut video.Cut, characters *characterkit.Characters, referenceImagesSupported bool) bool {
	req := video.GenerationRequest{
		ImageReference:  cut.KeyframeReference,
		ReferenceImages: video.CutReferenceImages(cut, characters),
	}
	caps := Capabilities{ReferenceImages: referenceImagesSupported}
	return ClassifyRequest(req, false, caps) == ModeReferenceToVideo
}

// SplitCutBySupportedDurations splits one cut into a run of sub-cuts with supported
// durations. Generated cuts are returned unchanged so their metadata does not drift
// from the actual video's duration. allowedDurations is the list of candidate
// durations to use, decided beforehand by the caller with AllowedCutDurations ({8}
// for reference_to_video, {4,6,8} for image_to_video). CutIndex is not renumbered
// (it is used from paths, such as scene splitting, where the caller has its own
// numbering; use ExpandCutsToSupportedDurations for normalizing the whole thing).
func SplitCutBySupportedDurations(cut video.Cut, allowedDurations []float64) []video.Cut {
	if cut.IsGenerated() {
		return []video.Cut{cut}
	}
	duration := cut.EffectiveDurationSec()
	if duration <= MaxCutDurationSec {
		cut.DurationSec = SnapDuration(duration, allowedDurations)
		cut.EndSec = cut.StartSec + cut.DurationSec
		return []video.Cut{cut}
	}

	// Split greedily at the 8-second cap and round only the trailing remainder to
	// a supported duration.
	// Note: when the remainder is rounded up, the last sub-cut's EndSec exceeds the
	// original cut.EndSec and may overlap the following cut on the timeline. That
	// is no problem for Veo's per-cut generation, so it is accepted by design.
	var durations []float64
	for remaining := duration; remaining > 0; {
		d := MaxCutDurationSec
		if remaining < MaxCutDurationSec {
			d = SnapDuration(remaining, allowedDurations)
		}
		durations = append(durations, d)
		remaining -= d
	}
	return buildSubCutsFromDurations(cut, durations)
}

// buildSubCutsFromDurations expands one parent cut into a run of sub-cuts
// matching durations. Only how the list of sub-durations is computed (greedy
// 8-second cap vs [base, 7s, ...]) differs between callers; the assembly from
// there on — carrying over parent fields, clearing chain flags on all but the
// first, laying out time contiguously, distributing lyrics evenly — is shared.
func buildSubCutsFromDurations(cut video.Cut, durations []float64) []video.Cut {
	subCuts := make([]video.Cut, 0, len(durations))
	offset := 0.0
	for i, d := range durations {
		sub := cut
		if i > 0 {
			sub.IsChainStart = false
			sub.IsSectionStart = false
		}
		sub.StartSec = cut.StartSec + offset
		sub.DurationSec = d
		sub.EndSec = sub.StartSec + d
		subCuts = append(subCuts, sub)
		offset += d
	}
	lines := SplitDialogueLines(cut.Dialogue)
	for i := range subCuts {
		subCuts[i].Dialogue = DistributeLines(lines, i, len(subCuts))
	}
	return subCuts
}

// splitChainCutIntoSupportedDurations splits a chain block pre-planned by the
// scene splitter (with IsChainStart, its duration one of the ChainDurations
// candidates) into the actual run of generated cuts [base, 7s, 7s, ...]. The
// first sub-cut is the chain's starting point, and its duration is chosen from
// allowedBases. The sub-cuts after it are video_extension's fixed 7 seconds.
//
// If a block duration that assumes a {4,6} base (11 seconds, etc.) was planned for
// a reference_to_video cut (base fixed at 8 seconds), the base is rounded to
// allowedBases and the achieved duration drifts a few seconds from the plan, but
// the planner picks its candidates with the same CutUsesReferenceImages decision,
// so the two normally agree.
func splitChainCutIntoSupportedDurations(cut video.Cut, allowedBases []float64) []video.Cut {
	duration := cut.EffectiveDurationSec()
	if duration <= MaxCutDurationSec {
		cut.DurationSec = SnapDuration(duration, allowedBases)
		cut.EndSec = cut.StartSec + cut.DurationSec
		return []video.Cut{cut}
	}
	extensions := int(math.Ceil((duration - MaxCutDurationSec) / VideoExtensionDurationSec))
	base := SnapDuration(duration-float64(extensions)*VideoExtensionDurationSec, allowedBases)

	// Build the run of sub-durations [base, 7s × extensions].
	durations := make([]float64, 0, extensions+1)
	durations = append(durations, base)
	for range extensions {
		durations = append(durations, VideoExtensionDurationSec)
	}
	return buildSubCutsFromDurations(cut, durations)
}

// CapCutsTotalDuration truncates the cuts from the one that exceeds maxSec onward,
// so the total duration does not exceed maxSec. At least the first cut is kept.
func CapCutsTotalDuration(cuts []video.Cut, maxSec float64) []video.Cut {
	total := 0.0
	for i, cut := range cuts {
		if i > 0 && total+cut.DurationSec > maxSec {
			return cuts[:i]
		}
		total += cut.DurationSec
	}
	return cuts
}

// ChainTailEnd returns the position of the last cut that must be regenerated
// together with the target cut when the target is regenerated.
//
// In a continuation chain, regenerating the target cut's video leaves the
// following cuts' PreviousVideoURI pointing at the old video. So everything up
// to just before the next chain starting point is regenerated together. With
// usePreviousVideo=false, cuts are not connected by video, so it stops at the
// target cut alone.
func ChainTailEnd(cuts []video.Cut, target int, usePreviousVideo bool) int {
	if !usePreviousVideo {
		return target
	}
	end := target
	for j := target + 1; j < len(cuts); j++ {
		if isChainBase(cuts[j]) {
			break
		}
		end = j
	}
	return end
}

// isChainBase reports whether this cut is a chain's starting point (base).
// Pre-planned chains carry IsChainStart, but in old recipes greedy splitting makes
// a non-7-second duration the base, so both clues are checked.
func isChainBase(cut video.Cut) bool {
	return cut.IsChainStart || cut.DurationSec != VideoExtensionDurationSec
}

// SplitDialogueLines breaks lyric text into a slice of lines, dropping empty lines.
func SplitDialogueLines(dialogue string) []string {
	var lines []string
	for line := range strings.SplitSeq(dialogue, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// DistributeLines apportions lines into total buckets and returns the pos-th bucket
// joined with newlines. Earlier buckets receive earlier lines, so the lyrics flow
// into consecutive sub-cuts in their original order.
func DistributeLines(lines []string, pos, total int) string {
	if len(lines) == 0 {
		return ""
	}
	if total <= 1 {
		return strings.Join(lines, "\n")
	}
	n := len(lines)
	start := pos * n / total
	end := (pos + 1) * n / total
	if start >= n || start >= end {
		return ""
	}
	if end > n {
		end = n
	}
	return strings.Join(lines[start:end], "\n")
}
