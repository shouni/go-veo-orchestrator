package runner

import (
	"context"
	"fmt"
	"strings"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	"github.com/shouni/go-veo-orchestrator/ports"
)

// VideoTimelineRunner はキーフレーム生成結果を Veo へ順次流し込み、Video-to-Video の文脈を引き継ぎます。
type VideoTimelineRunner struct {
	keyframeRunner ports.CutKeyframeRunner
	videoRunner    ports.VideoRunner
	publisher      ports.VideoPublishRunner
}

// NewVideoTimelineRunner は動画生成オーケストレーターを初期化します。
func NewVideoTimelineRunner(
	keyframeRunner ports.CutKeyframeRunner,
	videoRunner ports.VideoRunner,
	publisher ports.VideoPublishRunner,
) *VideoTimelineRunner {
	return &VideoTimelineRunner{
		keyframeRunner: keyframeRunner,
		videoRunner:    videoRunner,
		publisher:      publisher,
	}
}

// Run はカットのキーフレームを生成し、前カットの VideoID を引き継ぎながら順次動画化します。
func (r *VideoTimelineRunner) Run(ctx context.Context, recipe *ports.VideoRecipe) ([]*ports.VideoResponse, error) {
	if recipe == nil {
		return nil, fmt.Errorf("VideoRecipe がありません")
	}
	if r.keyframeRunner == nil {
		return nil, fmt.Errorf("keyframe runner is required")
	}
	if r.videoRunner == nil {
		return nil, fmt.Errorf("video runner is required")
	}

	recipe.Normalize()
	keyframes, err := r.keyframeRunner.Run(ctx, recipe)
	if err != nil {
		return nil, fmt.Errorf("カットキーフレーム生成に失敗しました: %w", err)
	}
	if len(keyframes) != len(recipe.Cuts) {
		return nil, fmt.Errorf("生成されたキーフレーム数(%d)とカット数(%d)が一致しません", len(keyframes), len(recipe.Cuts))
	}

	responses := make([]*ports.VideoResponse, 0, len(recipe.Cuts))
	lastVideoID := previousVideoIDFromRecipe(recipe)

	for i := range recipe.Cuts {
		cut := &recipe.Cuts[i]
		req := buildVideoGenerationRequest(recipe, *cut, keyframes[i], lastVideoID)

		res, err := r.videoRunner.Run(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("cut %d の動画生成に失敗しました: %w", cut.CutIndex, err)
		}
		if res == nil {
			return nil, fmt.Errorf("cut %d の動画生成レスポンスが nil です", cut.CutIndex)
		}
		if res.CutIndex == 0 {
			res.CutIndex = cut.CutIndex
		}

		cut.VideoURL = res.CloudURL
		cut.VideoID = res.VideoID
		responses = append(responses, res)
		if res.VideoID != "" {
			lastVideoID = res.VideoID
		}
	}

	recipe.Panels = recipe.Cuts
	return responses, nil
}

// RunAndSave は動画生成後、VideoRecipe を video_music_meta.json として保存します。
func (r *VideoTimelineRunner) RunAndSave(ctx context.Context, recipe *ports.VideoRecipe, outputPath string) (*ports.VideoPlotResponse, error) {
	videos, err := r.Run(ctx, recipe)
	if err != nil {
		return nil, err
	}
	if r.publisher == nil {
		return &ports.VideoPlotResponse{Recipe: recipe, Videos: videos}, nil
	}

	metadata, err := r.publisher.Run(ctx, recipe, outputPath)
	if err != nil {
		return nil, err
	}

	return &ports.VideoPlotResponse{
		Recipe:   recipe,
		Videos:   videos,
		Metadata: metadata,
	}, nil
}

func buildVideoGenerationRequest(recipe *ports.VideoRecipe, cut ports.Cut, keyframe *imagePorts.ImageResponse, lastVideoID string) ports.VideoGenerationRequest {
	var imageData []byte
	var seed int64
	if keyframe != nil {
		imageData = keyframe.Data
		seed = keyframe.UsedSeed
	}
	if seed == 0 {
		seed = recipe.Seed
	}

	return ports.VideoGenerationRequest{
		Prompt:          buildVideoPrompt(recipe, cut),
		ImageReference:  cut.ReferenceURL,
		AudioReference:  cut.AudioReference,
		InputImage:      imageData,
		PreviousVideoID: lastVideoID,
		Seed:            seed,
		CutIndex:        cut.CutIndex,
		DurationSec:     cut.DurationSec,
	}
}

func buildVideoPrompt(recipe *ports.VideoRecipe, cut ports.Cut) string {
	parts := []string{
		strings.TrimSpace(cut.VisualAnchor),
	}
	if cut.AudioCue != "" {
		parts = append(parts, fmt.Sprintf("Synchronize motion and camera timing with audio cue: %s", cut.AudioCue))
	}
	if recipe.MusicRecipe.Style != "" {
		parts = append(parts, "Music style: "+recipe.MusicRecipe.Style)
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

func previousVideoIDFromRecipe(recipe *ports.VideoRecipe) string {
	if recipe == nil {
		return ""
	}
	for i := len(recipe.Cuts) - 1; i >= 0; i-- {
		if recipe.Cuts[i].VideoID != "" {
			return recipe.Cuts[i].VideoID
		}
	}
	return ""
}
