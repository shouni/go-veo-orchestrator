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

// --- PageGenerator Options ---

// PageOption は PageGenerator の設定を適用する関数型です。
type PageOption func(*PageGenerator)

// WithPageMaxConcurrency は、ページ生成の最大並列数を設定します。
func WithPageMaxConcurrency(value int64) PageOption {
	return func(g *PageGenerator) {
		if value > 0 {
			g.maxConcurrency = value
		}
	}
}

// WithPageRateInterval は、ページ生成のレートリミット間隔を設定します。
func WithPageRateInterval(d time.Duration) PageOption {
	return func(g *PageGenerator) {
		if d > 0 {
			g.rateInterval = d
		}
	}
}

// WithPageRateBurst は、ページ生成のバースト許容数を設定します。
func WithPageRateBurst(value int) PageOption {
	return func(g *PageGenerator) {
		if value > 0 {
			g.rateBurst = value
		}
	}
}

// WithMaxPanelsPerPage は、1ページあたりの最大パネル数を設定します。
func WithMaxPanelsPerPage(value int) PageOption {
	return func(g *PageGenerator) {
		if value > 0 {
			g.maxPanelsPerPage = value
		}
	}
}
