package ports

import (
	"time"
)

// デフォルト値の定義
const (
	DefaultGeminiModel        = "gemini-3-flash-preview"
	DefaultImageStandardModel = "gemini-3-pro-image-preview"
	DefaultImageQualityModel  = "gemini-3-pro-image-preview"
	DefaultMaxConcurrency     = 1
	DefaultStyleSuffix        = "Japanese anime style, official art, cel-shaded, clean line art, high-quality manga coloring, expressive eyes, vibrant colors, cinematic lighting, masterpiece, ultra-detailed, flat shading, clear character features, no 3D effect, high resolution"
)

// Config は Go Manga Kit の各 Runner を動作させるための基本設定です。
type Config struct {
	// --- AI Model Settings (Common) ---
	GeminiModel        string
	ImageStandardModel string // 標準・高速（パネル用）
	ImageQualityModel  string // 高品質・高知能（ページ用）

	// --- Generation Settings ---
	MaxConcurrency int
	RateInterval   time.Duration
	StyleSuffix    string

	// --- Layout Settings ---
	MaxPanelsPerPage int

	// --- Timeout & Retries ---
	RequestTimeout time.Duration
}

// ApplyDefaults は未設定（ゼロ値）の項目にデフォルト値を適用します。
func (c *Config) ApplyDefaults() {
	if c.GeminiModel == "" {
		c.GeminiModel = DefaultGeminiModel
	}
	if c.ImageStandardModel == "" {
		c.ImageStandardModel = DefaultImageStandardModel
	}
	if c.ImageQualityModel == "" {
		c.ImageQualityModel = DefaultImageQualityModel
	}
	if c.MaxConcurrency <= 0 {
		c.MaxConcurrency = DefaultMaxConcurrency
	}
	if c.StyleSuffix == "" {
		c.StyleSuffix = DefaultStyleSuffix
	}
}
