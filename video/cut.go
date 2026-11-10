// Package video defines the video recipe domain model (Recipe, Cut and their
// operations) and the data types of generation requests and responses. It is the
// bottom package of this repository and depends on nothing else in it.
package video

import (
	"maps"
	"slices"
	"strings"

	characterkit "github.com/shouni/go-character-kit/character"
)

// UniqueCharacterIDs extracts the distinct CharacterIDs from a slice of cuts.
func (cs Cuts) UniqueCharacterIDs() []string {
	set := make(map[string]struct{})
	for _, cut := range cs {
		if cut.CharacterID != "" {
			set[cut.CharacterID] = struct{}{}
		}
	}
	return slices.Sorted(maps.Keys(set))
}

// EffectiveDurationSec returns the cut's actual duration in seconds. For a recipe
// whose DurationSec is unset (0 or less), it derives the value from StartSec /
// EndSec, so code handling hand-written JSON or only partly normalized recipes need
// not write the same fallback every time. It returns 0 when neither determines it.
func (c Cut) EffectiveDurationSec() float64 {
	if c.DurationSec > 0 {
		return c.DurationSec
	}
	if c.EndSec > c.StartSec {
		return c.EndSec - c.StartSec
	}
	return 0
}

// ResetGeneration clears the generation state so that this cut is regenerated.
// Status goes back to pending, and VideoID / VideoURL / IsChainStart are cleared.
// When keepKeyframe is false, KeyframeReference and IsSectionStart are cleared as
// well, on the assumption that a new keyframe will be made for this cut. When true,
// both are kept, for calls that reuse the saved keyframe reference (such as
// regenerating the video of only some cuts of an existing job).
func (c *Cut) ResetGeneration(keepKeyframe bool) {
	c.Status = CutStatusPending
	c.VideoID = ""
	c.VideoURL = ""
	c.IsChainStart = false
	if !keepKeyframe {
		c.KeyframeReference = ""
		c.IsSectionStart = false
	}
}

// CutReferenceImages builds the URI list for this cut's referenceImages (Veo's
// reference_to_video, asset type, up to 3). The order is [character art,
// keyframe], and references that are blank are skipped. It returns nil when there
// is no reference at all, and the adapter then falls back to the keyframe as image
// input (image_to_video).
//
// For the character art, the variant matching this cut's aspect ratio
// (Cut.AspectRatio, propagated from Recipe.AspectRatio by Recipe.Normalize) is used
// when one exists (Character.ReferenceURLs). keyframe.Generator picks the same way
// with ReferenceURLFor; if this function alone pinned ReferenceURL and ignored the
// ratio, one request would carry "a keyframe with the right ratio" next to
// "character art with a different ratio", and the colors and prop positions Veo
// picks up from the references would wobble.
//
// This function is the only rule for building referenceImages. The side that
// builds requests (DefaultVideoRequestBuilder) and the side that classifies them
// (duration normalization and prompt selection via veo.ClassifyRequest) see the
// same list, so drift such as "the duration was rounded to 8 seconds assuming
// reference_to_video, but the actual request was image_to_video" cannot happen by
// construction.
func CutReferenceImages(cut Cut, characters *characterkit.Characters) []string {
	var refs []string
	if characters != nil {
		if char := characters.GetCharacter(cut.CharacterID); char != nil {
			if ref := char.ReferenceURLFor(cut.AspectRatio); ref != "" {
				refs = append(refs, ref)
			}
		}
	}
	if ref := strings.TrimSpace(cut.KeyframeReference); ref != "" {
		refs = append(refs, ref)
	}
	return refs
}

// NextLastFrameReference returns "the next cut's keyframe reference" to use as
// cs[i]'s end frame (the lastFrame of frames_to_video interpolation). Ending this
// cut on the next cut's start frame smooths the join at the cut boundary
// (composition, the character's look). It returns an empty string, so the cut is
// generated without an end frame, when:
//
//   - there is no next cut, or the next cut has no keyframe
//   - the next cut starts a section (IsSectionStart). That is a deliberate scene
//     change, so the current cut's picture is not pulled toward the next
//     section's composition
//   - the characters differ (they would morph into each other during interpolation)
//   - the keyframe is the same as the current cut's (cuts sharing one keyframe after
//     a duration split, etc.; forcing end frame = start frame kills the motion)
func (cs Cuts) NextLastFrameReference(i int) string {
	if i < 0 || i+1 >= len(cs) {
		return ""
	}
	cur, next := cs[i], cs[i+1]
	ref := strings.TrimSpace(next.KeyframeReference)
	if ref == "" || next.IsSectionStart {
		return ""
	}
	if !strings.EqualFold(strings.TrimSpace(cur.CharacterID), strings.TrimSpace(next.CharacterID)) {
		return ""
	}
	if ref == strings.TrimSpace(cur.KeyframeReference) {
		return ""
	}
	return ref
}

// FillAudioReference sets audioURL on the cuts whose AudioReference is unset. It
// is for attaching the audio given with a task to every cut of the recipe, and does
// not overwrite a reference a cut already states explicitly. An empty audioURL
// does nothing.
func (cs Cuts) FillAudioReference(audioURL string) {
	audioURL = strings.TrimSpace(audioURL)
	if audioURL == "" {
		return
	}
	for i := range cs {
		if strings.TrimSpace(cs[i].AudioReference) == "" {
			cs[i].AudioReference = audioURL
		}
	}
}

// FillCharacterID sets characterID on the cuts whose CharacterID is unset. It does
// not overwrite a character a cut already states explicitly. An empty characterID
// does nothing.
func (cs Cuts) FillCharacterID(characterID string) {
	characterID = strings.TrimSpace(characterID)
	if characterID == "" {
		return
	}
	for i := range cs {
		if strings.TrimSpace(cs[i].CharacterID) == "" {
			cs[i].CharacterID = characterID
		}
	}
}

// IndexOf returns the position (0-based) of the cut whose cut_index (the 1-based
// number in the recipe) is cutIndex, or -1 when there is none.
func (cs Cuts) IndexOf(cutIndex int) int {
	for i := range cs {
		if cs[i].CutIndex == cutIndex {
			return i
		}
	}
	return -1
}
