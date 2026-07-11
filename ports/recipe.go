package ports

import "github.com/shouni/go-gemini-client/lyria"

// VideoRecipe は ScriptRunner が生成する動画台本全体の構造です。
// Lyria の Music Recipe と各カットの Audio Cue / Visual Anchor を同じ JSON に保持し、
// Veo への音楽同期プロンプトと後段の決定論的な結合処理の入力にします。
type VideoRecipe struct {
	ProjectTitle string            `json:"project_title,omitempty"`
	Description  string            `json:"description,omitempty"`
	MusicRecipe  lyria.MusicRecipe `json:"music_recipe"`
	Cuts         []Cut             `json:"cuts"`
	// FinalVideoURL は、全チェーンをハードカットで1本に結合した完成動画のURLです。
	// チェーンの継続生成（video_extension）を使わないジョブでは空のままです。
	FinalVideoURL string `json:"final_video_url,omitempty"`
}

// MusicRecipe は Lyria の楽曲生成レシピです。
type MusicRecipe = lyria.MusicRecipe

// Section は Lyria の楽曲セクションです。
type Section = lyria.MusicSection

// Lyrics は Lyria の歌詞ドラフトです。
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
	// IsChainStart は、このカットが継続チェーンの新規起点（PreviousVideoIDを使わない
	// image_to_videoベース）であることを示します。累積尺がVeoのvideo_extension上限に
	// 達する手前でのチェーンリセット、セクション境界、またはジョブ内最初のチェーンの
	// 先頭で立ちます。
	IsChainStart bool `json:"is_chain_start,omitempty"`
	// IsSectionStart は、IsChainStartのうち特に「曲のセクションが変わったこと」による
	// リセットであることを示します（30秒上限による技術的なリセットとは区別する）。
	// この場合、直前チェーンの最終フレームを引き継がず、そのカット自身のキーフレーム
	// 参照（セクションごとの意図した絵）から生成します。
	IsSectionStart bool `json:"is_section_start,omitempty"`
}

// Cuts は Cut のスライスに対するカスタム型です。
type Cuts []Cut

// CutStatus はカットの動画生成状態です。
type CutStatus string

const (
	// CutStatusPending はカットの動画生成が未完了であることを示します。
	CutStatusPending CutStatus = "pending"
	// CutStatusGenerated はカットの動画生成が完了していることを示します。
	CutStatusGenerated CutStatus = "generated"
	// CutStatusFailed はカットの動画生成が失敗したことを示します。
	CutStatusFailed CutStatus = "failed"
)

// Normalize は Music Recipe 由来のカット生成とタイムライン補完を行います。
func (vr *VideoRecipe) Normalize() {
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
		current = vr.Cuts[i].EndSec
	}
}

func cutsFromSections(sections []lyria.MusicSection) []Cut {
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

// IsGenerated はカットが動画生成済みとして扱えるかを返します。
func (c Cut) IsGenerated() bool {
	return c.Status == CutStatusGenerated || (c.VideoID != "" && c.VideoURL != "")
}
