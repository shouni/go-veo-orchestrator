package runner

import (
	"context"
	"fmt"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	"github.com/shouni/go-veo-orchestrator/ports"
)

// VideoTimelineRunner はキーフレーム生成結果を Veo へ順次流し込み、Video-to-Video の文脈を引き継ぎます。
type VideoTimelineRunner struct {
	keyframeRunner ports.CutKeyframeRunner
	videoRunner    ports.VideoRunner
	publisher      ports.VideoPublishRunner
	requestBuilder VideoRequestBuilder
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
		requestBuilder: NewVideoRequestBuilder(),
	}
}

// Run はカットのキーフレームを生成し、前カットの VideoID を引き継ぎながら順次動画化します。
func (r *VideoTimelineRunner) Run(ctx context.Context, recipe *ports.VideoRecipe) ([]*ports.VideoResponse, error) {
	if err := r.validateRun(recipe); err != nil {
		return nil, err
	}
	recipe.Normalize()

	keyframes, err := r.prepareKeyframes(ctx, recipe)
	if err != nil {
		return nil, err
	}

	responses := make([]*ports.VideoResponse, 0, len(recipe.Cuts))
	lastVideoID := ""

	for i := range recipe.Cuts {
		res, err := r.runCut(ctx, recipe, &recipe.Cuts[i], keyframes[i], lastVideoID)
		if err != nil {
			return nil, err
		}
		responses = append(responses, res)
		lastVideoID = nextVideoID(lastVideoID, res)
	}

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

func (r *VideoTimelineRunner) validateRun(recipe *ports.VideoRecipe) error {
	if recipe == nil {
		return fmt.Errorf("VideoRecipe がありません")
	}
	if r.keyframeRunner == nil {
		return fmt.Errorf("keyframe runner is required")
	}
	if r.videoRunner == nil {
		return fmt.Errorf("video runner is required")
	}
	return nil
}

func (r *VideoTimelineRunner) prepareKeyframes(ctx context.Context, recipe *ports.VideoRecipe) ([]*imagePorts.ImageResponse, error) {
	if !requiresKeyframeGeneration(recipe) {
		return make([]*imagePorts.ImageResponse, len(recipe.Cuts)), nil
	}

	keyframes, err := r.keyframeRunner.Run(ctx, recipe)
	if err != nil {
		return nil, fmt.Errorf("カットキーフレーム生成に失敗しました: %w", err)
	}
	if len(keyframes) != len(recipe.Cuts) {
		return nil, fmt.Errorf("生成されたキーフレーム数(%d)とカット数(%d)が一致しません", len(keyframes), len(recipe.Cuts))
	}
	return keyframes, nil
}

func requiresKeyframeGeneration(recipe *ports.VideoRecipe) bool {
	for _, cut := range recipe.Cuts {
		if cut.IsGenerated() {
			continue
		}
		if cut.KeyframeReference == "" {
			return true
		}
	}
	return false
}

func (r *VideoTimelineRunner) runCut(
	ctx context.Context,
	recipe *ports.VideoRecipe,
	cut *ports.Cut,
	keyframe *imagePorts.ImageResponse,
	lastVideoID string,
) (*ports.VideoResponse, error) {
	if cut.IsGenerated() {
		return responseFromCut(*cut), nil
	}

	req := r.requestBuilder.Build(recipe, *cut, keyframe, lastVideoID)
	res, err := r.videoRunner.Run(ctx, req)
	if err != nil {
		cut.Status = ports.CutStatusFailed
		return nil, fmt.Errorf("cut %d の動画生成に失敗しました: %w", cut.CutIndex, err)
	}
	if res == nil {
		return nil, fmt.Errorf("cut %d の動画生成レスポンスが nil です", cut.CutIndex)
	}

	applyVideoResponse(cut, res)
	return res, nil
}

func responseFromCut(cut ports.Cut) *ports.VideoResponse {
	return &ports.VideoResponse{
		CloudURL:    cut.VideoURL,
		VideoID:     cut.VideoID,
		CutIndex:    cut.CutIndex,
		DurationSec: cut.DurationSec,
	}
}

func applyVideoResponse(cut *ports.Cut, res *ports.VideoResponse) {
	if res.CutIndex == 0 {
		res.CutIndex = cut.CutIndex
	}
	cut.VideoURL = res.CloudURL
	cut.VideoID = res.VideoID
	cut.Status = ports.CutStatusGenerated
}

func nextVideoID(current string, res *ports.VideoResponse) string {
	if res.VideoID == "" {
		return current
	}
	return res.VideoID
}
