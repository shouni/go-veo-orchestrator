package runner

import (
	"fmt"
	"strings"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	characterkit "github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-veo-orchestrator/ports"
)

// VideoRequestBuilder は動画生成リクエストをカット情報から組み立てます。
type VideoRequestBuilder interface {
	Build(recipe *ports.VideoRecipe, cut ports.Cut, keyframe *imagePorts.ImageResponse, previousVideoID string) ports.VideoGenerationRequest
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
func (b *DefaultVideoRequestBuilder) Build(recipe *ports.VideoRecipe, cut ports.Cut, keyframe *imagePorts.ImageResponse, previousVideoID string) ports.VideoGenerationRequest {
	var imageData []byte
	var seed int64
	if keyframe != nil {
		imageData = keyframe.Data
		seed = keyframe.UsedSeed
	}
	if seed == 0 && recipe.MusicRecipe.Seed != nil {
		seed = *recipe.MusicRecipe.Seed
	}

	return ports.VideoGenerationRequest{
		Prompt:          b.buildPrompt(recipe, cut),
		ImageReference:  cut.KeyframeReference,
		ReferenceImages: b.buildReferenceImages(cut),
		AudioReference:  cut.AudioReference,
		InputImage:      imageData,
		PreviousVideoID: previousVideoID,
		Seed:            seed,
		CutIndex:        cut.CutIndex,
		DurationSec:     cut.DurationSec,
	}
}

// buildReferenceImages はキャラクター立ち絵とキーフレームから referenceImages 用の
// GCS URI リストを組み立てます。characters 未設定、または参照が1つもない場合は nil を
// 返し、adapter 側はキーフレームの image 入力（image-to-video）へフォールバックします。
func (b *DefaultVideoRequestBuilder) buildReferenceImages(cut ports.Cut) []string {
	if b.characters == nil {
		return nil
	}
	char := b.characters.GetCharacter(strings.TrimSpace(cut.CharacterID))
	if char == nil {
		return nil
	}
	characterRef := strings.TrimSpace(char.ReferenceURL)
	if characterRef == "" {
		return nil
	}
	refs := []string{characterRef}
	if keyframeRef := strings.TrimSpace(cut.KeyframeReference); keyframeRef != "" {
		refs = append(refs, keyframeRef)
	}
	return refs
}

func (b *DefaultVideoRequestBuilder) buildPrompt(recipe *ports.VideoRecipe, cut ports.Cut) string {
	parts := []string{
		strings.TrimSpace(cut.VisualAnchor),
	}
	if cut.AudioCue != "" {
		parts = append(parts, fmt.Sprintf("Synchronize motion and camera timing with audio cue: %s", cut.AudioCue))
	}
	if musicMood := strings.TrimSpace(recipe.MusicRecipe.Mood); musicMood != "" {
		parts = append(parts, "Music mood: "+musicMood)
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
