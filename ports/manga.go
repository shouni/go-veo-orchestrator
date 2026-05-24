package ports

import imagePorts "github.com/shouni/gemini-image-kit/ports"

// VideoRecipe は ScriptRunner が生成する動画台本全体の構造です。
// Music Recipe と各カットの Audio Cue / Visual Anchor を同じ JSON に保持し、
// Veo への音楽同期プロンプトと後段の決定論的な結合処理の入力にします。
type VideoRecipe struct {
	ProjectTitle string      `json:"project_title"`
	Title        string      `json:"title,omitempty"` // 旧 manga JSON 互換。Normalize 後は ProjectTitle と同期します。
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
	Panels       []Cut       `json:"panels,omitempty"` // 旧 manga JSON 互換。Normalize 後は Cuts と同期します。
}

// MusicRecipe は BGM 全体のテンポ・尺・スタイルを表す楽曲構成書です。
type MusicRecipe struct {
	TempoBPM         int     `json:"tempo_bpm"`
	TotalDurationSec float64 `json:"total_duration_sec"`
	Style            string  `json:"style"`
}

// Section は楽曲生成JSON内の可変長セクションです。
// Verse / Chorus などの曲展開を、そのまま動画タイムラインの候補カットとして扱えます。
type Section struct {
	Name            string  `json:"name"`
	DurationSeconds float64 `json:"duration_seconds"`
	Prompt          string  `json:"prompt"`
}

// Lyrics は楽曲生成JSON内の歌詞・テーマ情報です。
type Lyrics struct {
	Title     string   `json:"title"`
	Theme     string   `json:"theme"`
	Hook      string   `json:"hook"`
	Lyrics    string   `json:"lyrics"`
	Keywords  []string `json:"keywords"`
	Mood      string   `json:"mood"`
	Narrative string   `json:"narrative"`
}

// Cut は動画内の1カットを表します。
// audio_cue は BGM 上の展開、visual_anchor は映像上の固定指示です。
type Cut struct {
	CutIndex     int     `json:"cut_index"`
	DurationSec  float64 `json:"duration_sec"`
	AudioCue     string  `json:"audio_cue"`
	VisualAnchor string  `json:"visual_anchor"`
	CharacterID  string  `json:"character_id"`
	Dialogue     string  `json:"dialogue,omitempty"`
	ReferenceURL string  `json:"reference_url,omitempty"` // キーフレーム画像などの参照。
	VideoURL     string  `json:"video_url,omitempty"`
	StartSec     float64 `json:"start_sec,omitempty"`
	EndSec       float64 `json:"end_sec,omitempty"`

	// 旧 manga JSON 互換フィールド。
	Page      int    `json:"page,omitempty"`
	SpeakerID string `json:"speaker_id,omitempty"`
}

// Cuts は Cut のスライスに対するカスタム型です。
type Cuts []Cut

// Scene は複数カットを束ねる時間軸上のまとまりです。
type Scene struct {
	SceneNumber int
	VideoURL    string
	Cuts        []Cut
}

// Normalize は旧 JSON 互換フィールドと動画用フィールドを同期し、各カットの開始/終了秒を補完します。
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
	if vr.MusicRecipe.TempoBPM == 0 {
		vr.MusicRecipe.TempoBPM = vr.Tempo
	}
	if vr.Tempo == 0 {
		vr.Tempo = vr.MusicRecipe.TempoBPM
	}
	if vr.MusicRecipe.Style == "" {
		vr.MusicRecipe.Style = vr.Mood
	}
	if vr.Mood == "" {
		vr.Mood = vr.MusicRecipe.Style
	}
	if len(vr.Cuts) == 0 && len(vr.Panels) == 0 && len(vr.Sections) > 0 {
		vr.Cuts = cutsFromSections(vr.Sections)
	}
	if len(vr.Cuts) == 0 && len(vr.Panels) > 0 {
		vr.Cuts = vr.Panels
	}
	if len(vr.Panels) == 0 && len(vr.Cuts) > 0 {
		vr.Panels = vr.Cuts
	}

	var current float64
	for i := range vr.Cuts {
		vr.Cuts[i].Normalize(i+1, current)
		current = vr.Cuts[i].EndSec
	}
	vr.Panels = vr.Cuts
	if vr.MusicRecipe.TotalDurationSec == 0 {
		vr.MusicRecipe.TotalDurationSec = current
	}
}

func cutsFromSections(sections []Section) []Cut {
	cuts := make([]Cut, 0, len(sections))
	for i, section := range sections {
		cuts = append(cuts, Cut{
			CutIndex:     i + 1,
			DurationSec:  section.DurationSeconds,
			AudioCue:     section.Prompt,
			VisualAnchor: section.Name,
		})
	}
	return cuts
}

// Normalize は旧 JSON 互換フィールドと動画用フィールドを同期し、時間範囲を補完します。
func (c *Cut) Normalize(index int, startSec float64) {
	if c.CutIndex == 0 {
		c.CutIndex = index
	}
	if c.CharacterID == "" {
		c.CharacterID = c.SpeakerID
	}
	if c.SpeakerID == "" {
		c.SpeakerID = c.CharacterID
	}
	if c.StartSec == 0 {
		c.StartSec = startSec
	}
	if c.EndSec == 0 {
		c.EndSec = c.StartSec + c.DurationSec
	}
}

// MangaResponse は旧 API 互換のエイリアスです。新規コードでは VideoRecipe を使用してください。
type MangaResponse = VideoRecipe

// Panel は旧 API 互換のエイリアスです。新規コードでは Cut を使用してください。
type Panel = Cut

// Panels は旧 API 互換のエイリアスです。新規コードでは Cuts を使用してください。
type Panels = Cuts

// Page は旧 API 互換の構造体です。新規コードでは Scene を使用してください。
type Page struct {
	PageNumber int
	ImageURL   string
	Panels     []Panel
}

// ResourceMap は、文字やパネルのリソースファイルをインデックスや順序付きの参照にマッピングするための構造体です。
type ResourceMap struct {
	// CharacterFiles は SpeakerID から OrderedAssets のインデックスへのマップです。
	CharacterFiles map[string]int
	// PanelFiles は ReferenceURL から OrderedAssets のインデックスへのマップです。
	PanelFiles map[string]int
	// OrderedAssets は Gemini に渡す画像アセット（File API URI と元の URL のペア）の順序付きリストです。
	OrderedAssets []imagePorts.ImageURI
}
