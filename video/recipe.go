package video

import (
	"fmt"
	"strings"

	"github.com/shouni/go-gemini-client/music"
)

// Recipe は ScriptRunner が生成する動画台本全体の構造です。
// Lyria の Music Recipe と各カットの Audio Cue / Visual Anchor を同じ JSON に保持し、
// Veo への音楽同期プロンプトと後段の決定論的な結合処理の入力にします。
type Recipe struct {
	ProjectTitle string `json:"project_title,omitempty"`
	Description  string `json:"description,omitempty"`
	// LocationAnchor is the single persistent core setting (location plus any recurring prop —
	// e.g. "a misty coastal cliffside road overlooking the ocean at dawn; her bicycle beside
	// her") for the entire video. It is decided once at script-generation time and propagated
	// onto every Cut by Normalize. Keyframe generation runs each cut independently and in
	// parallel (see go-veo-orchestrator/keyframe.Generator.Execute), and prompt builders such as
	// ports.KeyframePrompt.BuildCut only ever see a single Cut, not the parent Recipe — so
	// without this field, a cut whose own VisualAnchor omits the location (e.g. a tight emotional
	// close-up) has nothing grounding its background, and the image model is free to hallucinate
	// an unrelated one.
	LocationAnchor string       `json:"location_anchor,omitempty"`
	MusicRecipe    music.Recipe `json:"music_recipe"`
	Cuts           []Cut        `json:"cuts"`
	// FinalVideoURL は、全チェーンをハードカットで1本に結合した完成動画のURLです。
	// チェーンの継続生成（video_extension）を使わないジョブでは空のままです。
	FinalVideoURL string `json:"final_video_url,omitempty"`
	// AspectRatio は、このレシピのキーフレーム・動画生成に使われたアスペクト比です
	// （例: "16:9", "9:16"）。キーフレーム作成時に一度だけ決まり、以降の動画生成
	// （フルMV・ショート・カット再生成いずれも）はこの値をそのまま使います。
	AspectRatio string `json:"aspect_ratio,omitempty"`
}

// MusicRecipe は楽曲生成レシピです（music.Recipe の別名）。
type MusicRecipe = music.Recipe

// Section は楽曲セクションです（music.Section の別名）。
type Section = music.Section

// Lyrics は歌詞ドラフトです（music.LyricsDraft の別名）。
type Lyrics = music.LyricsDraft

// NewRecipeFromMusic は、Music Recipe から動画レシピを組み立てます。
//
// タイトルの相互補完とセクション→カット展開は Normalize が行うため、この関数の
// 仕事は音楽レシピを深いコピーで取り込んで正規化することだけです。以前は消費者側が
// 「タイトルを写して Normalize を呼ぶだけ」の変換関数を自前で持ち、その際の
// フィールド単位の手書きコピーが Seed / AudioModel / ComposeMode を静かに
// 取り落としていました。深いコピー（music.Recipe.Clone）を使うことで、音楽側に
// フィールドが増えても取りこぼしが構造的に起こりません。
func NewRecipeFromMusic(musicRecipe music.Recipe) *Recipe {
	vr := &Recipe{MusicRecipe: *musicRecipe.Clone()}
	vr.Normalize()
	return vr
}

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
	// KeyframeSeed は、保存済みキーフレームの生成に使われたシードです。
	//
	// 画像そのものはレシピに載らず GCS 上のファイルとして残るため、シードをここへ
	// 記録しておかないと「どの条件で焼いた絵か」が失われます。動画生成もこの値を
	// 引き継いでキーフレームと同じ乱数系列を使います（未記録の 0 の場合は
	// キャラクター／レシピのシードへフォールバックします）。
	KeyframeSeed int64 `json:"keyframe_seed,omitempty"`
}

// Result は、カットの Veo 動画生成結果を保持します。
type Result struct {
	VideoURL string    `json:"video_url,omitempty"`
	VideoID  string    `json:"video_id,omitempty"`
	Status   CutStatus `json:"status,omitempty"`
}

// IsGenerated はカットが動画生成済みとして扱えるかを返します。
func (r Result) IsGenerated() bool {
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
// 生成結果・制御フラグは種別ごとに AudioSync / KeyframeResult / Result / ChainControl
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
	// LocationAnchor mirrors Recipe.LocationAnchor for this cut. It is populated by
	// Recipe.Normalize, not meant to be set independently per cut, and exists only so that
	// prompt builders operating on a single Cut (ports.KeyframePrompt.BuildCut) can still ground
	// their keyframe prompt in the video's persistent setting.
	LocationAnchor string `json:"location_anchor,omitempty"`
	CharacterID    string `json:"character_id"`
	Dialogue       string `json:"dialogue,omitempty"`

	AudioSync
	KeyframeResult
	Result
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

// Validate は Recipe が後段の処理に耐える状態かを検証します。
//
// Normalize が埋められる欠落は Normalize に任せ、ここでは埋めようのない破綻だけを見ます。
// AI 生成の台本は JSON Schema で型と必須項目までしか縛れず、cuts が空になることや
// section_index が音楽のセクション数を超えることは文法制約の外側で起こります。
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
		// SectionIndex は1始まりで、0は「未割り当て」を意味する正当な値。
		// 範囲外の非ゼロ値だけを不正とする。
		if cut.SectionIndex < 0 || cut.SectionIndex > numSections {
			return fmt.Errorf("%w: cut %d has out-of-range section_index %d (recipe has %d sections)", ErrRecipeInvalid, cut.CutIndex, cut.SectionIndex, numSections)
		}
	}
	return nil
}

// firstNonEmptyString は、空白を除いて最初に中身のある文字列を返します。
func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
