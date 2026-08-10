// Package ports は、go-veo-orchestrator の各コンポーネントが依存する
// インターフェース（ポート）と、動画生成に関する共通データ型・設定を定義します。
package ports

import (
	"fmt"
	"strings"
	"time"
)

// DefaultMaxConcurrency などは Config に適用するデフォルト値です。
const (
	DefaultMaxConcurrency = 1
	// DefaultRateBurst は、キーフレーム生成レート制限の既定バースト数です。
	DefaultRateBurst = 1
)

// Config は Go Veo Orchestrator の各 Runner を動作させるための基本設定です。
type Config struct {
	// --- AI Model Settings (Common) ---
	// どちらも必須です（Validate 参照）。
	GeminiModel string
	ImageModel  string

	// --- Generation Settings ---
	MaxConcurrency int
	RateInterval   time.Duration
	// RateBurst はキーフレーム生成レート制限のバースト許容数です（0 以下は既定値 1）。
	RateBurst int
	// KeyframeAspectRatio はキーフレーム画像生成のアスペクト比です（例: "16:9", "9:16"）。
	// 空文字の場合は keyframe.CutAspectRatio（既定値）が使われます。
	KeyframeAspectRatio string
}

// ApplyDefaults は未設定（ゼロ値）の項目にデフォルト値を適用します。
func (c *Config) ApplyDefaults() {
	if c.MaxConcurrency <= 0 {
		c.MaxConcurrency = DefaultMaxConcurrency
	}
	if c.RateBurst <= 0 {
		c.RateBurst = DefaultRateBurst
	}
}

// Validate はモデル名が指定されていることを検証します。
func (c *Config) Validate() error {
	missing := make([]string, 0, 2)
	if strings.TrimSpace(c.GeminiModel) == "" {
		missing = append(missing, "GeminiModel")
	}
	if strings.TrimSpace(c.ImageModel) == "" {
		missing = append(missing, "ImageModel")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: %s must be set (this library has no default model names)", ErrConfigInvalid, strings.Join(missing, ", "))
	}
	return nil
}

// WithModels は、指定されたモデル名で上書きした Config のコピーを返します。
func (c Config) WithModels(geminiModel, imageModel string) Config {
	if geminiModel = strings.TrimSpace(geminiModel); geminiModel != "" {
		c.GeminiModel = geminiModel
	}
	if imageModel = strings.TrimSpace(imageModel); imageModel != "" {
		c.ImageModel = imageModel
	}
	c.ApplyDefaults()
	return c
}

// WithAspectRatio は、指定されたアスペクト比で上書きした Config のコピーを返します。
// 空文字は「変更なし」として扱います。
func (c Config) WithAspectRatio(aspectRatio string) Config {
	if aspectRatio = strings.TrimSpace(aspectRatio); aspectRatio != "" {
		c.KeyframeAspectRatio = aspectRatio
	}
	return c
}

// UsesModels は、指定モデルを適用しても現在の Config と同じモデル構成かを返します。
func (c Config) UsesModels(geminiModel, imageModel string) bool {
	current := c
	current.ApplyDefaults()
	selected := c.WithModels(geminiModel, imageModel)
	return current.GeminiModel == selected.GeminiModel &&
		current.ImageModel == selected.ImageModel
}
