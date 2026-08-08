package runner

import (
	"context"
	"fmt"

	"github.com/shouni/go-veo-orchestrator/ports"
)

// CutObserver は、1カットの動画生成が完了するたびに呼ばれるフックです。
//
// resumable な実行のために用意されています: 呼び出し側はここでカットごとの
// 途中経過（メタデータの保存、実行時間予算の確認など）を行い、エラーを返すと
// Run はそこで停止して**それまでの部分結果**を返します。時間制限のあるジョブ基盤で
// 「期限が近いので一旦保存して次の実行で再開する」という運用が、Run を自前の
// ループで置き換えずにできます。
type CutObserver func(ctx context.Context, cut *ports.Cut, res *ports.VideoResponse) error

// VideoTimelineRunner はキーフレーム生成結果を Veo へ順次流し込み、Video-to-Video の文脈を引き継ぎます。
type VideoTimelineRunner struct {
	keyframeRunner ports.CutKeyframeRunner
	videoRunner    ports.VideoRunner
	requestBuilder VideoRequestBuilder
	observer       CutObserver
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

// WithCutObserver は、カット完了ごとのフックを設定します。
// nil を渡した場合は変更せず、メソッドチェーンできるよう自身を返します。
func (r *VideoTimelineRunner) WithCutObserver(observer CutObserver) *VideoTimelineRunner {
	if observer != nil {
		r.observer = observer
	}
	return r
}

// Run はカットのキーフレームを生成し、前カットの VideoID を引き継ぎながら順次動画化します。
//
// エラー時も、それまでに完了したカットのレスポンスを部分結果として返します。
// 1カットの生成には分単位の時間と実費が掛かるため、途中で失敗しても完了分の
// 情報は呼び出し側が保存・再開に使えます（レシピの Cut には生成結果が反映済みです）。
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
			return responses, err
		}
		responses = append(responses, res)
		lastVideoID = nextVideoID(lastVideoID, res)

		if r.observer != nil {
			if err := r.observer(ctx, &recipe.Cuts[i], res); err != nil {
				return responses, fmt.Errorf("cut %d の後処理で停止しました: %w", recipe.Cuts[i].CutIndex, err)
			}
		}
	}

	return responses, nil
}

func (r *VideoTimelineRunner) validateRun(recipe *ports.VideoRecipe) error {
	if recipe == nil {
		return ports.ErrRecipeRequired
	}
	if r.keyframeRunner == nil {
		return ports.ErrKeyframeRunnerRequired
	}
	if r.videoRunner == nil {
		return ports.ErrVideoRunnerRequired
	}
	return nil
}

func (r *VideoTimelineRunner) prepareKeyframes(ctx context.Context, recipe *ports.VideoRecipe) ([]*ports.KeyframeImage, error) {
	if !requiresKeyframeGeneration(recipe) {
		return make([]*ports.KeyframeImage, len(recipe.Cuts)), nil
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
	keyframe *ports.KeyframeImage,
	lastVideoID string,
	caps ports.VeoCapabilities,
) (*ports.VideoResponse, error) {
	cut := &recipe.Cuts[cutIndex]
	if cut.IsGenerated() {
		return responseFromCut(*cut), nil
	}

	// IsChainStart のカットは video-to-video チェーンの新規起点なので、直前カットの
	// VideoID を PreviousVideoURI として引き継がず、チェーンをここでリセットする
	// （セクション境界や継続尺の上限到達によるリセット。ports.ChainControl 参照）。
	previousVideoURI := lastVideoID
	if cut.IsChainStart {
		previousVideoURI = ""
	}

	req := r.requestBuilder.Build(BuildInput{
		Recipe:             recipe,
		Cut:                *cut,
		Keyframe:           keyframe,
		PreviousVideoURI:   previousVideoURI,
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
		// nil レスポンスも失敗として扱う。pending のまま残すと、resume 側が
		// 「まだ実行していないカット」と誤認する。
		cut.Status = ports.CutStatusFailed
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
	mode := ports.ClassifyVeoRequest(req, req.PreviousVideoURI != "", caps)
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
