package runner

import (
	"fmt"
	"strings"

	characterkit "github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-veo-orchestrator/veo"
	"github.com/shouni/go-veo-orchestrator/video"
)

// BuildInput は、1カット分の動画生成リクエストを組み立てるための入力一式です。
// 以前の位置引数（recipe, cut, keyframe, previousVideoID）では lastFrame やモデルの
// 対応機能を渡す先が無く、呼び出し側が Build() の戻り値を後から書き換える前提に
// なっていたため、構造体で受け取る形にしています。
type BuildInput struct {
	// Recipe はカットが属するレシピです（プロンプトの Music mood / Seed に使います）。
	Recipe *video.Recipe
	// Cut は生成対象のカットです。
	Cut video.Cut
	// PreviousVideoURI は video-to-video 継続の文脈として引き継ぐ前カット動画の
	// gs:// URI です。空文字はチェーンの起点（継続しない）を意味します。
	PreviousVideoURI string
	// LastFrameReference は frames_to_video 補間の終了フレームです
	// （通常は Cuts.NextLastFrameReference の戻り値）。
	LastFrameReference string
	// Capabilities は実際に使う VideoRunner／モデルが対応している Veo のオプション機能です
	// （veo.RunnerCapabilities で導出）。ゼロ値は「オプション機能なし」として扱い、
	// image_to_video 側へ倒れます。
	Capabilities veo.Capabilities
}

// VideoRequestBuilder は動画生成リクエストをカット情報から組み立てます。
type VideoRequestBuilder interface {
	Build(in BuildInput) video.GenerationRequest
}

// DefaultVideoRequestBuilder は標準の動画生成リクエストビルダーです。
// characters が設定されている場合、カットのキャラクター立ち絵とキーフレームを
// ReferenceImages（Veo の referenceImages、asset タイプ・最大3枚）として組み立てます。
type DefaultVideoRequestBuilder struct {
	characters *characterkit.Characters
}

// NewVideoRequestBuilder は DefaultVideoRequestBuilder を初期化します。
func NewVideoRequestBuilder() *DefaultVideoRequestBuilder {
	return &DefaultVideoRequestBuilder{}
}

// NewVideoRequestBuilderWithCharacters は、キャラクター立ち絵を referenceImages として
// 組み立てるビルダーを初期化します。characters が nil の場合は標準ビルダーと同じ挙動です。
func NewVideoRequestBuilderWithCharacters(characters *characterkit.Characters) *DefaultVideoRequestBuilder {
	return &DefaultVideoRequestBuilder{characters: characters}
}

// Build はレシピ、カット、キーフレーム生成結果から Veo 用リクエストを構築します。
//
// 組み立てたリクエストは veo.ClassifyRequest で分類し、そのモードで Veo が実際には
// 使わない入力を落としてから返します（例: video_extension では画像参照を送らない、
// lastFrame 非対応モデルでは LastFrameReference を残さない）。「組み立てたリクエスト」と
// 「adapter が Veo へ送るリクエスト」を一致させることで、ログや再現時に実際には送られて
// いない入力を追いかけずに済みます。
func (b *DefaultVideoRequestBuilder) Build(in BuildInput) video.GenerationRequest {
	cut := in.Cut

	// キーフレームは保存済みで、参照（KeyframeReference）とシード（KeyframeSeed）が
	// カットに記録されている。同じ乱数系列を動画側へ引き継ぐためにそのシードを使い、
	// 記録が無い場合だけキャラクター／レシピのシードへ落とす。
	seed := cut.KeyframeSeed
	if seed == 0 {
		seed = fallbackSeed(in.Recipe, cut, b.characters)
	}

	// PreviousVideoURI は gs:// の動画参照だけが意味を持つ。オペレーション名などが
	// 紛れ込むと、分類は image_to_video に倒れるのにフィールドだけが残り、adapter が
	// 独自判定で video_extension を組んでしまう余地が生まれるため、ここで除去する。
	previousVideoURI := strings.TrimSpace(in.PreviousVideoURI)
	if !strings.HasPrefix(previousVideoURI, "gs://") {
		previousVideoURI = ""
	}

	req := video.GenerationRequest{
		ImageReference:     cut.KeyframeReference,
		ReferenceImages:    video.CutReferenceImages(cut, b.characters),
		AudioReference:     cut.AudioReference,
		PreviousVideoURI:   previousVideoURI,
		LastFrameReference: in.LastFrameReference,
		Seed:               seed,
		CutIndex:           cut.CutIndex,
		DurationSec:        cut.DurationSec,
	}

	// PreviousVideoURI が空 = 「このカットはチェーンの起点なので継続しない」という
	// 呼び出し側の意思表示。空でないときだけ video_extension の候補になる。
	usePreviousVideo := previousVideoURI != ""
	pruneUnusedInputs(&req, veo.ClassifyRequest(req, usePreviousVideo, in.Capabilities))
	req.Prompt = b.buildPrompt(in.Recipe, cut)

	return req
}

// pruneUnusedInputs は、解決した生成モードで Veo が参照しない入力をリクエストから
// 落とします。Veo は video と referenceImages / image を併用できず、lastFrame は image と
// セットのときだけ有効なため、残しておいても無視されるだけで、ログ上は「送ったのに
// 効かなかった入力」に見えてしまいます。
func pruneUnusedInputs(req *video.GenerationRequest, mode veo.GenerationMode) {
	switch mode {
	case veo.ModeVideoExtension:
		req.ReferenceImages = nil
		req.ImageReference = ""
		req.InputImage = nil
		req.LastFrameReference = ""
	case veo.ModeReferenceToVideo:
		req.LastFrameReference = ""
		req.PreviousVideoURI = ""
	case veo.ModeFramesToVideo:
		req.ReferenceImages = nil
		req.PreviousVideoURI = ""
	case veo.ModeImageToVideo:
		req.ReferenceImages = nil
		req.LastFrameReference = ""
		req.PreviousVideoURI = ""
	}
}

// fallbackSeed は、キーフレーム生成結果からシードが得られなかった場合のシードを返します。
// カットのキャラクターに紐づくシード（キーフレーム画像生成が使うのと同じ値）を優先し、
// 無ければレシピ全体のシードを使います。シード無し（0 = Veo リクエストから省略）の
// 独立生成はチェーン起点ごとに見た目が確率的にブレるため、少なくともキャラクター単位で
// 固定したシードを渡してキャラの一貫性を高めます。
func fallbackSeed(recipe *video.Recipe, cut video.Cut, characters *characterkit.Characters) int64 {
	if characters != nil {
		if char := characters.GetCharacter(strings.TrimSpace(cut.CharacterID)); char != nil && char.Seed != nil {
			return *char.Seed
		}
	}
	if recipe != nil && recipe.MusicRecipe.Seed != nil {
		return *recipe.MusicRecipe.Seed
	}
	return 0
}

func (b *DefaultVideoRequestBuilder) buildPrompt(recipe *video.Recipe, cut video.Cut) string {
	parts := []string{
		strings.TrimSpace(cut.VisualAnchor),
	}
	if cut.AudioCue != "" {
		parts = append(parts, fmt.Sprintf("Synchronize motion and camera timing with audio cue: %s", cut.AudioCue))
	}
	if recipe != nil {
		if musicMood := strings.TrimSpace(recipe.MusicRecipe.Mood); musicMood != "" {
			parts = append(parts, "Music mood: "+musicMood)
		}
	}
	if cut.StartSec != 0 || cut.EndSec != 0 {
		parts = append(parts, fmt.Sprintf("Timeline: %.2fs to %.2fs.", cut.StartSec, cut.EndSec))
	}

	nonEmpty := parts[:0]
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			nonEmpty = append(nonEmpty, part)
		}
	}
	return strings.Join(nonEmpty, "\n")
}
