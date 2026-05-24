package layout

import "time"

// --- PanelGenerator Options ---

// PanelOption は PanelGenerator の設定を適用する関数型です。
type PanelOption func(*PanelGenerator)

// WithPanelMaxConcurrency は、パネル生成の最大並列数を設定します。
func WithPanelMaxConcurrency(value int) PanelOption {
	return func(g *PanelGenerator) {
		if value > 0 {
			g.maxConcurrency = value
		}
	}
}

// WithPanelRateInterval は、パネル生成のレートリミット間隔を設定します。
func WithPanelRateInterval(d time.Duration) PanelOption {
	return func(g *PanelGenerator) {
		if d > 0 {
			g.rateInterval = d
		}
	}
}

// WithPanelRateBurst は、パネル生成のバースト許容数を設定します。
func WithPanelRateBurst(value int) PanelOption {
	return func(g *PanelGenerator) {
		if value > 0 {
			g.rateBurst = value
		}
	}
}
