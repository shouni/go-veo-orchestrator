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
	g.aspectRatio = CutAspectRatio
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

// WithAspectRatio は、生成するキーフレーム画像のアスペクト比を設定します
// （例: "16:9", "9:16"）。空文字の場合は既定値（CutAspectRatio）のまま変更しません。
func WithAspectRatio(value string) Option {
	return func(g *Generator) {
		if value != "" {
			g.aspectRatio = value
		}
	}
}
