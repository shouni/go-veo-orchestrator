package keyframe

import (
	"time"

	"github.com/shouni/go-veo-orchestrator/ports"
)

// Option は Generator の設定を適用する関数型です。
type Option func(*Generator)

func applyDefaultOptions(g *Generator) {
	g.maxConcurrency = ports.DefaultMaxConcurrency
	g.rateInterval = defaultRateInterval
	g.rateBurst = defaultRateBurst
}

// WithMaxConcurrency は、キーフレーム生成の最大並列数を設定します。
func WithMaxConcurrency(value int) Option {
	return func(g *Generator) {
		if value > 0 {
			g.maxConcurrency = value
		}
	}
}

// WithRateInterval は、キーフレーム生成のレートリミット間隔を設定します。
func WithRateInterval(d time.Duration) Option {
	return func(g *Generator) {
		if d > 0 {
			g.rateInterval = d
		}
	}
}

// WithRateBurst は、キーフレーム生成のバースト許容数を設定します。
func WithRateBurst(value int) Option {
	return func(g *Generator) {
		if value > 0 {
			g.rateBurst = value
		}
	}
}

// WithEditModel は、EditCut（既存キーフレームの部分編集）で使う画像編集モデルを設定します。
// 未設定のままだと EditCut はエラーを返します。
func WithEditModel(model string) Option {
	return func(g *Generator) {
		if model != "" {
			g.editModel = model
		}
	}
}
