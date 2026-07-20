package ports

import "github.com/shouni/go-gemini-client/lyria"

// VideoRecipe は ScriptRunner が生成する動画台本全体の構造です。
// Lyria の Music Recipe と各カットの Audio Cue / Visual Anchor を同じ JSON に保持し、
// Veo への音楽同期プロンプトと後段の決定論的な結合処理の入力にします。
type VideoRecipe struct {
	ProjectTitle string `json:"project_title,omitempty"`
	Description  string `json:"description,omitempty"`
	// LocationAnchor is the single persistent core setting (location plus any recurring prop —
	// e.g. "a misty coastal cliffside road overlooking the ocean at dawn; her bicycle beside
	// her") for the entire video. It is decided once at script-generation time and propagated
	// onto every Cut by Normalize. Keyframe generation runs each cut independently and in
	// parallel (see go-veo-orchestrator/keyframe.Generator.Execute), and prompt builders such as
	// ports.KeyframePrompt.BuildCut only ever see a single Cut, not the parent VideoRecipe — so
	// without this field, a cut whose own VisualAnchor omits the location (e.g. a tight emotional
	// close-up) has nothing grounding its background, and the image model is free to hallucinate
	// an unrelated one.
	LocationAnchor string            `json:"location_anchor,omitempty"`
	MusicRecipe    lyria.MusicRecipe `json:"music_recipe"`
	Cuts           []Cut             `json:"cuts"`
	// FinalVideoURL は、全チェーンをハードカットで1本に結合した完成動画のURLです。
	// チェーンの継続生成（video_extension）を使わないジョブでは空のままです。
	FinalVideoURL string `json:"final_video_url,omitempty"`
	// AspectRatio は、このレシピのキーフレーム・動画生成に使われたアスペクト比です
	// （例: "16:9", "9:16"）。キーフレーム作成時に一度だけ決まり、以降の動画生成
	// （フルMV・ショート・カット再生成いずれも）はこの値をそのまま使います。
	AspectRatio string `json:"aspect_ratio,omitempty"`
}

// MusicRecipe は Lyria の楽曲生成レシピです。
type MusicRecipe = lyria.MusicRecipe

// Section は Lyria の楽曲セクションです。
type Section = lyria.MusicSection

// Lyrics は Lyria の歌詞ドラフトです。
type Lyrics = lyria.LyricsDraft

// AudioSync は、カットを楽曲のタイムラインに同期させるための情報を保持します。
type AudioSync struct {
	DurationSec    float64 `json:"duration_sec"`
	AudioCue       string  `json:"audio_cue"`
	AudioReference string  `json:"audio_reference,omitempty"`
	StartSec       float64 `json:"start_sec,omitempty"`
	EndSec         float64 `json:"end_sec,omitempty"`
}

// KeyframeResult は、カットのキーフレーム（静止画）生成結果を保持します。
type KeyframeResult struct {
	KeyframeReference string `json:"keyframe_reference,omitempty"`
}

// VideoResult は、カットの Veo 動画生成結果を保持します。
type VideoResult struct {
	VideoURL string    `json:"video_url,omitempty"`
	VideoID  string    `json:"video_id,omitempty"`
	Status   CutStatus `json:"status,omitempty"`
}

// IsGenerated はカットが動画生成済みとして扱えるかを返します。
func (r VideoResult) IsGenerated() bool {
	return r.Status == CutStatusGenerated || (r.VideoID != "" && r.VideoURL != "")
}

// ChainControl は、カットが Veo の video-to-video チェーンにどう接続するかを決めるフラグを
// 保持します。
type ChainControl struct {
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

// Cut は動画内の1カットを表します。
// audio_cue は BGM 上の展開、visual_anchor は映像上の固定指示です。
// 生成結果・制御フラグは種別ごとに AudioSync / KeyframeResult / VideoResult / ChainControl
// へ分割し、匿名フィールドとして埋め込んでいます。JSON はフラットな構造のまま維持され、
// cut.VideoID のようなフィールドアクセスも変わりません。
type Cut struct {
	CutIndex int `json:"cut_index"`
	// SectionIndex は、このカットが属する MusicRecipe.Sections の1始まりの位置です
	// （0 は未割り当て）。同じセクション由来のカットがシーン分割で複数のサブカットに
	// 分かれても、分割後の全サブカットが同じ SectionIndex を引き継ぎます。呼び出し側は
	// StartSec とセクションの時間範囲を突き合わせて逆算せずに、この値で直接セクションの
	// 所属を判定できます。
	SectionIndex int    `json:"section_index,omitempty"`
	VisualAnchor string `json:"visual_anchor"`
	// LocationAnchor mirrors VideoRecipe.LocationAnchor for this cut. It is populated by
	// VideoRecipe.Normalize, not meant to be set independently per cut, and exists only so that
	// prompt builders operating on a single Cut (ports.KeyframePrompt.BuildCut) can still ground
	// their keyframe prompt in the video's persistent setting.
	LocationAnchor string `json:"location_anchor,omitempty"`
	CharacterID    string `json:"character_id"`
	Dialogue       string `json:"dialogue,omitempty"`

	AudioSync
	KeyframeResult
	VideoResult
	ChainControl
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
		if vr.Cuts[i].SectionIndex == 0 {
			vr.Cuts[i].SectionIndex = sectionIndexForStartSec(vr.MusicRecipe.Sections, vr.Cuts[i].StartSec)
		}
		if vr.Cuts[i].LocationAnchor == "" {
			vr.Cuts[i].LocationAnchor = vr.LocationAnchor
		}
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
			SectionIndex: i + 1,
			VisualAnchor: section.Name,
			AudioSync: AudioSync{
				DurationSec: duration,
				AudioCue:    section.Prompt,
			},
		})
	}
	return cuts
}

// sectionIndexForStartSec は、startSec を含む時間範囲を持つセクションの1始まりの位置を返します
// （該当なしの場合は 0）。Cut.SectionIndex が未設定（0）のまま Normalize が呼ばれた場合の
// フォールバックとしてのみ使われ、明示的に設定された SectionIndex を上書きしません。
// 一致するセクションが複数ありうる境界値では、startSec 以下で最大の StartSeconds を持つ
// セクションを採用します（EndSeconds との間の丸め誤差に頑健にするため）。
func sectionIndexForStartSec(sections []lyria.MusicSection, startSec float64) int {
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
