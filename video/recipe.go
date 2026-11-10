package video

import (
	"fmt"
	"strings"

	"github.com/shouni/genai-kit/music"
)

// Recipe is the whole video script that ScriptRunner generates. It keeps Lyria's
// Music Recipe and each cut's Audio Cue / Visual Anchor in the same JSON, as input
// to the music-synchronized prompts for Veo and to the deterministic joining done
// downstream.
type Recipe struct {
	ProjectTitle string `json:"project_title,omitempty"`
	Description  string `json:"description,omitempty"`
	// LocationAnchor is the location setting that stays consistent across the
	// whole video (the place, plus props that keep appearing; e.g. "a misty
	// coastal cliffside road overlooking the ocean at dawn; her bicycle beside
	// her"). It is decided once during script generation, and Normalize
	// propagates it to every Cut.
	//
	// Keyframe generation runs per cut, and prompt builders such as
	// ports.KeyframePrompt.BuildCut see only a single Cut, never the parent
	// Recipe. Without this field, nothing constrains the background of a cut
	// whose VisualAnchor names no place (a close-up of an expression, say), and
	// the image model invents an unrelated location.
	LocationAnchor string       `json:"location_anchor,omitempty"`
	MusicRecipe    music.Recipe `json:"music_recipe"`
	Cuts           []Cut        `json:"cuts"`
	// FinalVideoURL is the URL of the finished video, with every chain joined into
	// one by hard cuts. It stays empty for jobs that do not use chain continuation
	// (video_extension).
	FinalVideoURL string `json:"final_video_url,omitempty"`
	// AspectRatio is the aspect ratio used for this recipe's keyframe and video
	// generation (e.g. "16:9", "9:16"). It is decided once when the keyframes are
	// made, and all later video generation (full MV, short, cut regeneration)
	// uses this value as-is.
	AspectRatio string `json:"aspect_ratio,omitempty"`
}

// MusicRecipe is the music generation recipe (an alias of music.Recipe).
type MusicRecipe = music.Recipe

// Section is a song section (an alias of music.Section).
type Section = music.Section

// Lyrics is a lyrics draft (an alias of music.LyricsDraft).
type Lyrics = music.LyricsDraft

// NewRecipeFromMusic builds a video recipe from a Music Recipe.
//
// Filling the titles in from each other and expanding sections into cuts is done by
// Normalize, so this function's only job is to take the music recipe in as a deep
// copy and normalize it. Consumers used to keep their own conversion function that
// "just copies the title and calls Normalize", and its hand-written field-by-field
// copy silently dropped Seed / AudioModel / ComposeMode. Using a deep copy
// (music.Recipe.Clone) means fields added on the music side cannot be dropped, by
// construction.
func NewRecipeFromMusic(musicRecipe music.Recipe) *Recipe {
	vr := &Recipe{MusicRecipe: *musicRecipe.Clone()}
	vr.Normalize()
	return vr
}

// AudioSync holds what synchronizes a cut to the song's timeline.
type AudioSync struct {
	DurationSec    float64 `json:"duration_sec"`
	AudioCue       string  `json:"audio_cue"`
	AudioReference string  `json:"audio_reference,omitempty"`
	StartSec       float64 `json:"start_sec,omitempty"`
	EndSec         float64 `json:"end_sec,omitempty"`
}

// KeyframeResult holds the result of generating a cut's keyframe (still image).
type KeyframeResult struct {
	KeyframeReference string `json:"keyframe_reference,omitempty"`
	// KeyframeSeed is the seed used to generate the saved keyframe.
	//
	// The image itself is not carried in the recipe but stays as a file on GCS, so
	// unless the seed is recorded here, "under which conditions was this picture
	// baked" is lost. Video generation also carries this value over and uses the
	// same random sequence as the keyframe (when it is an unrecorded 0, it falls
	// back to the character's / recipe's seed).
	KeyframeSeed int64 `json:"keyframe_seed,omitempty"`
}

// Result holds the result of a cut's Veo video generation.
type Result struct {
	VideoURL string    `json:"video_url,omitempty"`
	VideoID  string    `json:"video_id,omitempty"`
	Status   CutStatus `json:"status,omitempty"`
}

// IsGenerated reports whether the cut can be treated as having its video generated.
func (r Result) IsGenerated() bool {
	return r.Status == CutStatusGenerated || (r.VideoID != "" && r.VideoURL != "")
}

// ChainControl holds the flags that decide how a cut connects into Veo's
// video-to-video chain.
type ChainControl struct {
	// IsChainStart marks this cut as a fresh start of a continuation chain (an
	// image_to_video base that does not use PreviousVideoID). It is set for a
	// chain reset just before the cumulative duration reaches Veo's
	// video_extension limit, at a section boundary, or at the head of the first
	// chain in the job.
	IsChainStart bool `json:"is_chain_start,omitempty"`
	// IsSectionStart marks, among IsChainStart cuts, a reset caused specifically
	// by "the song's section changed" (as opposed to a technical reset due to
	// the 30-second limit). In this case the previous chain's last frame is not
	// carried over; the cut is generated from its own keyframe reference (the
	// picture intended for each section).
	IsSectionStart bool `json:"is_section_start,omitempty"`
}

// Cut is one cut in the video.
// audio_cue is what happens in the BGM, visual_anchor the fixed visual direction.
// Generation results and control flags are split by kind into AudioSync /
// KeyframeResult / Result / ChainControl and embedded as anonymous fields. The JSON
// stays flat, and field access such as cut.VideoID is unchanged.
type Cut struct {
	CutIndex int `json:"cut_index"`
	// SectionIndex is the 1-based position in MusicRecipe.Sections that this cut
	// belongs to (0 means unassigned). When cuts from the same section are split
	// into several sub-cuts by a scene split, every resulting sub-cut inherits the
	// same SectionIndex. Callers can determine section membership directly from
	// this value instead of reverse-matching StartSec against the sections' time
	// ranges.
	SectionIndex int    `json:"section_index,omitempty"`
	VisualAnchor string `json:"visual_anchor"`
	// LocationAnchor is a copy of Recipe.LocationAnchor. Recipe.Normalize fills it;
	// it is not meant to be set per cut (see Recipe.LocationAnchor for why).
	LocationAnchor string `json:"location_anchor,omitempty"`
	// AspectRatio is a copy of Recipe.AspectRatio, filled by Recipe.Normalize for
	// the same reason as LocationAnchor: CutReferenceImages also sees only a
	// single Cut, and needs this value to pick the ratio-matched character
	// reference image (Character.ReferenceURLFor).
	AspectRatio string `json:"aspect_ratio,omitempty"`
	CharacterID string `json:"character_id"`
	Dialogue    string `json:"dialogue,omitempty"`

	AudioSync
	KeyframeResult
	Result
	ChainControl
}

// Cuts is a custom type over a slice of Cut.
type Cuts []Cut

// CutStatus is a cut's video generation state.
type CutStatus string

const (
	// CutStatusPending means the cut's video generation has not completed.
	CutStatusPending CutStatus = "pending"
	// CutStatusGenerated means the cut's video generation has completed.
	CutStatusGenerated CutStatus = "generated"
	// CutStatusFailed means the cut's video generation failed.
	CutStatusFailed CutStatus = "failed"
)

// Normalize generates cuts from the Music Recipe and fills in the timeline.
func (vr *Recipe) Normalize() {
	if vr == nil {
		return
	}
	if vr.ProjectTitle == "" {
		vr.ProjectTitle = vr.MusicRecipe.Title
	}
	if vr.MusicRecipe.Title == "" {
		vr.MusicRecipe.Title = vr.ProjectTitle
	}
	if len(vr.Cuts) == 0 && len(vr.MusicRecipe.Sections) > 0 {
		vr.Cuts = cutsFromSections(vr.MusicRecipe.Sections)
	}

	var current float64
	for i := range vr.Cuts {
		vr.Cuts[i].Normalize(i+1, current)
		if vr.Cuts[i].SectionIndex == 0 {
			vr.Cuts[i].SectionIndex = sectionIndexForStartSec(vr.MusicRecipe.Sections, vr.Cuts[i].StartSec)
		}
		if vr.Cuts[i].LocationAnchor == "" {
			vr.Cuts[i].LocationAnchor = vr.LocationAnchor
		}
		if vr.Cuts[i].AspectRatio == "" {
			vr.Cuts[i].AspectRatio = vr.AspectRatio
		}
		current = vr.Cuts[i].EndSec
	}
}

func cutsFromSections(sections []music.Section) []Cut {
	cuts := make([]Cut, 0, len(sections))
	for i, section := range sections {
		duration := float64(section.Duration)
		if duration == 0 && section.EndSeconds > section.StartSeconds {
			duration = float64(section.EndSeconds - section.StartSeconds)
		}
		cuts = append(cuts, Cut{
			CutIndex:     i + 1,
			SectionIndex: i + 1,
			VisualAnchor: section.Name,
			DurationSec:  duration,
			AudioCue:     section.Prompt,
		})
	}
	return cuts
}

// sectionIndexForStartSec returns the 1-based position of the section whose time
// range contains startSec (0 when none does). It is used only as the fallback when
// Normalize is called with Cut.SectionIndex unset (0), and never overwrites an
// explicitly set SectionIndex. At boundary values where several sections could
// match, it takes the section with the largest StartSeconds not exceeding startSec
// (to be robust against rounding error against EndSeconds).
func sectionIndexForStartSec(sections []music.Section, startSec float64) int {
	bestIndex := -1
	bestStart := -1.0
	for i, s := range sections {
		start := float64(s.StartSeconds)
		if start <= startSec && start >= bestStart {
			bestIndex = i
			bestStart = start
		}
	}
	if bestIndex == -1 {
		return 0
	}
	return bestIndex + 1
}

// Normalize fills in the cut numbers and time ranges.
func (c *Cut) Normalize(index int, startSec float64) {
	if c.CutIndex == 0 {
		c.CutIndex = index
	}
	if c.Status == "" {
		c.Status = CutStatusPending
	}
	if c.StartSec == 0 {
		c.StartSec = startSec
	}
	if c.EndSec == 0 {
		c.EndSec = c.StartSec + c.DurationSec
	}
}

// Validate checks that the Recipe is in a state the downstream steps can handle.
//
// Gaps that Normalize can fill are left to Normalize; this looks only for breakage
// that cannot be filled. A JSON Schema can constrain an AI-generated script only up
// to types and required fields; an empty cuts list, or a section_index beyond the
// number of song sections, happens outside what the grammar constrains.
func (vr *Recipe) Validate() error {
	if vr == nil {
		return fmt.Errorf("%w: video recipe is nil", ErrRecipeInvalid)
	}
	if strings.TrimSpace(firstNonEmptyString(vr.ProjectTitle, vr.MusicRecipe.Title)) == "" {
		return fmt.Errorf("%w: video recipe title is required", ErrRecipeInvalid)
	}
	if len(vr.Cuts) == 0 {
		return fmt.Errorf("%w: video recipe requires cuts", ErrRecipeInvalid)
	}

	numSections := len(vr.MusicRecipe.Sections)
	for _, cut := range vr.Cuts {
		// SectionIndex is 1-based, and 0 is a legitimate value meaning "unassigned".
		// Only a non-zero out-of-range value is invalid.
		if cut.SectionIndex < 0 || cut.SectionIndex > numSections {
			return fmt.Errorf("%w: cut %d has out-of-range section_index %d (recipe has %d sections)", ErrRecipeInvalid, cut.CutIndex, cut.SectionIndex, numSections)
		}
	}
	return nil
}

// firstNonEmptyString returns the first string that has content after trimming whitespace.
func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
