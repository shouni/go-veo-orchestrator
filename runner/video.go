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
	requestBuilder VideoRequestBuilder
}

// NewVideoTimelineRunner は動画生成オーケストレーターを初期化します。
func NewVideoTimelineRunner(
	keyframeRunner ports.CutKeyframeRunner,
	videoRunner ports.VideoRunner,
) *VideoTimelineRunner {
	return &VideoTimelineRunner{
		keyframeRunner: keyframeRunner,
		videoRunner:    videoRunner,
		requestBuilder: NewVideoRequestBuilder(),
	}
}

// WithRequestBuilder は動画生成リクエストの組み立てを差し替えます。
// nil を渡した場合は変更せず、メソッドチェーンできるよう自身を返します。
func (r *VideoTimelineRunner) WithRequestBuilder(builder VideoRequestBuilder) *VideoTimelineRunner {
	if builder != nil {
		r.requestBuilder = builder
	}
	return r
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

	// 使用する VideoRunner が対応している Veo のオプション機能は全カットで共通なので
	// ループの外で一度だけ導出する。
	caps := ports.RunnerCapabilities(r.videoRunner)

	responses := make([]*ports.VideoResponse, 0, len(recipe.Cuts))
	lastVideoID := ""

	for i := range recipe.Cuts {
		res, err := r.runCut(ctx, recipe, i, keyframes[i], lastVideoID, caps)
		if err != nil {
			return nil, err
		}
		responses = append(responses, res)
		lastVideoID = nextVideoID(lastVideoID, res)
	}

	return responses, nil
}

func (r *VideoTimelineRunner) validateRun(recipe *ports.VideoRecipe) error {
	if recipe == nil {
		return ports.ErrRecipeRequired
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
	if err := mustMatchCutCount("生成されたキーフレーム数", len(keyframes), len(recipe.Cuts)); err != nil {
		return nil, err
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
	cutIndex int,
	keyframe *imagePorts.ImageResponse,
	lastVideoID string,
	caps ports.VeoCapabilities,
) (*ports.VideoResponse, error) {
	cut := &recipe.Cuts[cutIndex]
	if cut.IsGenerated() {
		return responseFromCut(*cut), nil
	}

	// IsChainStart のカットは video-to-video チェーンの新規起点なので、直前カットの
	// VideoID を PreviousVideoID として引き継がず、チェーンをここでリセットする
	// （セクション境界や継続尺の上限到達によるリセット。ports.ChainControl 参照）。
	previousVideoID := lastVideoID
	if cut.IsChainStart {
		previousVideoID = ""
	}

	req := r.requestBuilder.Build(BuildInput{
		Recipe:             recipe,
		Cut:                *cut,
		Keyframe:           keyframe,
		PreviousVideoID:    previousVideoID,
		LastFrameReference: ports.Cuts(recipe.Cuts).NextLastFrameReference(cutIndex),
		Capabilities:       caps,
	})
	if err := validateCutDuration(req, caps); err != nil {
		cut.Status = ports.CutStatusFailed
		return nil, err
	}

	res, err := r.videoRunner.Run(ctx, req)
	if err != nil {
		cut.Status = ports.CutStatusFailed
		return nil, fmt.Errorf("cut %d の動画生成に失敗しました: %w", cut.CutIndex, err)
	}
	if res == nil {
		return nil, fmt.Errorf("cut %d の動画生成レスポンスが nil です", cut.CutIndex)
	}

	applyVideoResponse(cut.CutIndex, &cut.VideoResult, res)
	return res, nil
}

// validateCutDuration は、これから Veo へ送るリクエストの尺が、そのリクエストが解決する
// 生成モードで受け付けられる値かを検証します。
//
// Veo は任意長の動画を生成できず、モードごとに離散的な尺しか受け付けません
// （image_to_video なら {4,6,8}、reference_to_video は8秒固定、video_extension は7秒固定）。
// 検証せずに送ると Veo 側で拒否されますが、それが分かるのは長時間実行オペレーションを
// 投げて待った後です。ここで手前に落とすことで、レシピ側の尺の計画ミスをカット生成の
// 待ち時間と課金の前に、どのカットが何秒でどのモードだったかまで示して報告できます。
func validateCutDuration(req ports.VideoGenerationRequest, caps ports.VeoCapabilities) error {
	mode := ports.ClassifyVeoRequest(req, req.PreviousVideoID != "", caps)
	if ports.IsSupportedDuration(req.DurationSec, mode) {
		return nil
	}
	return fmt.Errorf("cut %d: %.2f秒は %s では生成できません（対応する尺: %v）: %w",
		req.CutIndex, req.DurationSec, mode, ports.DurationsForMode(mode), ports.ErrUnsupportedCutDuration)
}

func responseFromCut(cut ports.Cut) *ports.VideoResponse {
	return &ports.VideoResponse{
		CloudURL:    cut.VideoURL,
		VideoID:     cut.VideoID,
		CutIndex:    cut.CutIndex,
		DurationSec: cut.DurationSec,
	}
}

// applyVideoResponse は、動画生成結果 (res) をカットの VideoResult に反映します。
// cutIndex 以外は VideoResult フィールドしか読み書きしないことをシグネチャで示しています。
func applyVideoResponse(cutIndex int, result *ports.VideoResult, res *ports.VideoResponse) {
	if res.CutIndex == 0 {
		res.CutIndex = cutIndex
	}
	result.VideoURL = res.CloudURL
	result.VideoID = res.VideoID
	result.Status = ports.CutStatusGenerated
}

func nextVideoID(current string, res *ports.VideoResponse) string {
	if res.VideoID == "" {
		return current
	}
	return res.VideoID
}
