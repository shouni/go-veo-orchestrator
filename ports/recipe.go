package ports

import "github.com/shouni/go-gemini-client/lyria"

// VideoRecipe は ScriptRunner が生成する動画台本全体の構造です。
// Music Recipe と各カットの Audio Cue / Visual Anchor を同じ JSON に保持し、
// Veo への音楽同期プロンプトと後段の決定論的な結合処理の入力にします。
type VideoRecipe struct {
	ProjectTitle string      `json:"project_title"`
	Title        string      `json:"title,omitempty"`
	Theme        string      `json:"theme,omitempty"`
	Mood         string      `json:"mood,omitempty"`
	Tempo        int         `json:"tempo,omitempty"`
	Instruments  []string    `json:"instruments,omitempty"`
	Sections     []Section   `json:"sections,omitempty"`
	Lyrics       *Lyrics     `json:"lyrics,omitempty"`
	AudioModel   string      `json:"audio_model,omitempty"`
	ComposeMode  string      `json:"compose_mode,omitempty"`
	Seed         int64       `json:"seed,omitempty"`
	Description  string      `json:"description,omitempty"`
	MusicRecipe  MusicRecipe `json:"music_recipe"`
	Cuts         []Cut       `json:"cuts"`
}

type MusicRecipe = lyria.MusicRecipe

type Section = lyria.MusicSection

type Lyrics = lyria.LyricsDraft

// Cut は動画内の1カットを表します。
// audio_cue は BGM 上の展開、visual_anchor は映像上の固定指示です。
type Cut struct {
	CutIndex          int       `json:"cut_index"`
	DurationSec       float64   `json:"duration_sec"`
	AudioCue          string    `json:"audio_cue"`
	AudioReference    string    `json:"audio_reference,omitempty"`
	VisualAnchor      string    `json:"visual_anchor"`
	CharacterID       string    `json:"character_id"`
	Dialogue          string    `json:"dialogue,omitempty"`
	KeyframeReference string    `json:"keyframe_reference,omitempty"`
	VideoURL          string    `json:"video_url,omitempty"`
	VideoID           string    `json:"video_id,omitempty"`
	Status            CutStatus `json:"status,omitempty"`
	StartSec          float64   `json:"start_sec,omitempty"`
	EndSec            float64   `json:"end_sec,omitempty"`
}

// Cuts は Cut のスライスに対するカスタム型です。
type Cuts []Cut

type CutStatus string

const (
	CutStatusPending   CutStatus = "pending"
	CutStatusGenerated CutStatus = "generated"
	CutStatusFailed    CutStatus = "failed"
)

// Normalize はトップレベルの楽曲JSONと動画生成用JSONの差を吸収し、
// section 由来のカット生成とタイムライン補完を行います。
func (vr *VideoRecipe) Normalize() {
	if vr == nil {
		return
	}
	if vr.ProjectTitle == "" {
		vr.ProjectTitle = vr.Title
	}
	if vr.Title == "" {
		vr.Title = vr.ProjectTitle
	}
	vr.syncMusicRecipe()
	if len(vr.Cuts) == 0 && len(vr.Sections) > 0 {
		vr.Cuts = cutsFromSections(vr.Sections)
	}

	var current float64
	for i := range vr.Cuts {
		vr.Cuts[i].Normalize(i+1, current)
		current = vr.Cuts[i].EndSec
	}
}

func (vr *VideoRecipe) syncMusicRecipe() {
	if vr.MusicRecipe.Title == "" {
		vr.MusicRecipe.Title = vr.Title
	}
	if vr.Title == "" {
		vr.Title = vr.MusicRecipe.Title
	}
	if vr.MusicRecipe.Theme == "" {
		vr.MusicRecipe.Theme = vr.Theme
	}
	if vr.Theme == "" {
		vr.Theme = vr.MusicRecipe.Theme
	}
	if vr.MusicRecipe.Tempo == 0 {
		vr.MusicRecipe.Tempo = vr.Tempo
	}
	if vr.Tempo == 0 {
		vr.Tempo = vr.MusicRecipe.Tempo
	}
	if vr.MusicRecipe.Mood == "" {
		vr.MusicRecipe.Mood = vr.Mood
	}
	if vr.Mood == "" {
		vr.Mood = vr.MusicRecipe.Mood
	}
	if len(vr.MusicRecipe.Instruments) == 0 {
		vr.MusicRecipe.Instruments = vr.Instruments
	}
	if len(vr.Instruments) == 0 {
		vr.Instruments = vr.MusicRecipe.Instruments
	}
	if len(vr.MusicRecipe.Sections) == 0 {
		vr.MusicRecipe.Sections = vr.Sections
	}
	if len(vr.Sections) == 0 {
		vr.Sections = vr.MusicRecipe.Sections
	}
	if vr.MusicRecipe.Lyrics == nil {
		vr.MusicRecipe.Lyrics = vr.Lyrics
	}
	if vr.Lyrics == nil {
		vr.Lyrics = vr.MusicRecipe.Lyrics
	}
	if vr.MusicRecipe.AudioModel == "" {
		vr.MusicRecipe.AudioModel = vr.AudioModel
	}
	if vr.AudioModel == "" {
		vr.AudioModel = vr.MusicRecipe.AudioModel
	}
	if vr.MusicRecipe.ComposeMode == "" {
		vr.MusicRecipe.ComposeMode = vr.ComposeMode
	}
	if vr.ComposeMode == "" {
		vr.ComposeMode = vr.MusicRecipe.ComposeMode
	}
	if vr.MusicRecipe.Seed == nil && vr.Seed != 0 {
		seed := vr.Seed
		vr.MusicRecipe.Seed = &seed
	}
	if vr.Seed == 0 && vr.MusicRecipe.Seed != nil {
		vr.Seed = *vr.MusicRecipe.Seed
	}
}

func cutsFromSections(sections []Section) []Cut {
	cuts := make([]Cut, 0, len(sections))
	for i, section := range sections {
		duration := float64(section.Duration)
		if duration == 0 && section.EndSeconds > section.StartSeconds {
			duration = float64(section.EndSeconds - section.StartSeconds)
		}
		cuts = append(cuts, Cut{
			CutIndex:     i + 1,
			DurationSec:  duration,
			AudioCue:     section.Prompt,
			VisualAnchor: section.Name,
		})
	}
	return cuts
}

// Normalize はカット番号と時間範囲を補完します。
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

func (c Cut) IsGenerated() bool {
	return c.Status == CutStatusGenerated || (c.VideoID != "" && c.VideoURL != "")
}
