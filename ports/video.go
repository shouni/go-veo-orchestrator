package ports

import (
	"context"

	"github.com/shouni/go-veo-orchestrator/video"
)

// VideoRunner は Veo API を叩いて1カットの動画を生成・管理する契約です。
// 実装（Veo adapter）はこのリポジトリに含まれず、呼び出し側が注入します。
type VideoRunner interface {
	Run(ctx context.Context, req video.GenerationRequest) (*video.Response, error)
}

// ReferenceImagesSupporter は、reference_to_video（referenceImages、Veo 3系の非Fastモデル
// のみ対応で8秒固定）を使えるかを問い合わせられる VideoRunner のオプションインター
// フェースです。カットの尺を Veo のサポート値へ正規化する処理が、モデル判定ロジックを
// 重複実装せずに「このカットは8秒固定になるか」を判断するために使います。
//
// 実装するのは adapter なので、判定を組み立てる veo.RunnerCapabilities ではなく
// adapter 向けの契約が集まるこのパッケージに置いています。
type ReferenceImagesSupporter interface {
	SupportsReferenceImages() bool
}

// LastFrameSupporter は、frames_to_video（image + lastFrame の first/last frame 補間、
// Veo 2 / Veo 3.1 系のみ対応）を使えるかを問い合わせられる VideoRunner のオプション
// インターフェースです。
type LastFrameSupporter interface {
	SupportsLastFrame() bool
}

// VideoTimelineRunner は Music Recipe のカット列を順次動画化する契約です。
type VideoTimelineRunner interface {
	Run(ctx context.Context, r *video.Recipe) ([]*video.Response, error)
}

// noopVideoTimelineRunner は、常に ErrVideoRunnerNotConfigured で失敗する
// VideoTimelineRunner です。VideoRunner（Veo アダプター）が注入されなかった場合に
// Workflows.Video を nil にせずこれを入れることで、呼び出し側は初回利用時の nil パニックの
// 代わりに errors.Is で判定できるエラーを受け取ります。
type noopVideoTimelineRunner struct{}

// NewNoopVideoTimelineRunner は、常に ErrVideoRunnerNotConfigured を返す
// VideoTimelineRunner を返します。
func NewNoopVideoTimelineRunner() VideoTimelineRunner {
	return noopVideoTimelineRunner{}
}

func (noopVideoTimelineRunner) Run(context.Context, *video.Recipe) ([]*video.Response, error) {
	return nil, ErrVideoRunnerNotConfigured
}
