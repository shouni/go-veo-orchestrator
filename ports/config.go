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
	// DefaultRequestTimeout は AI 呼び出し1回あたりの既定の上限時間です。
	// 画像生成は分単位で掛かることがあるため、余裕を持った値にしています。
	DefaultRequestTimeout = 5 * time.Minute
)

// Config は Go Veo Orchestrator の各 Runner を動作させるための基本設定です。
//
// MaxConcurrency / RateInterval / RequestTimeout は AI 呼び出しの実行制御です。
// 発射間隔と上限時間は台本のテキスト生成とキーフレームの画像生成の**両方**に
// 掛かります（クォータはプロジェクト単位で、操作の種類ごとではないため）。
// 並列度が効くのはキーフレーム生成だけで、動画生成は Video-to-Video の連鎖上
// 必ず逐次です。
type Config struct {
	// --- AI Model Settings (Common) ---
	// どちらも必須です（Validate 参照）。
	GeminiModel string
	ImageModel  string

	// --- Generation Settings ---
	// MaxConcurrency はキーフレーム生成の同時実行数です（0 以下は既定値 1）。
	//
	// 注意: RateInterval が設定されていれば、スループットは並列度によらず
	// 1/RateInterval で頭打ちになります。両方を大きくする設定は矛盾しています。
	MaxConcurrency int
	// RateInterval は AI 呼び出しの発射間隔の下限です（0 で無制限）。
	RateInterval time.Duration
	// RequestTimeout は AI 呼び出し1回あたりの上限時間です（0 以下は既定値）。
	// レート制限の待機はこの上限の外側で行うため、混雑がタイムアウトに化けません。
	RequestTimeout time.Duration
	// --- Art Direction ---
	//
	// このライブラリは画作りの既定値を一切持ちません。アスペクト比も解像度も
	// ネガティブプロンプトも作品ごとに変わる値で、キットが既定を持つと呼び出し側の
	// 設定と二重の出所になり、片方だけ変えたときに黙って食い違います。

	// KeyframeAspectRatio はキーフレーム画像生成のアスペクト比です（例: "16:9", "9:16"）。
	// **必須です**（Validate 参照）。空のまま送ると、モデルが勝手に選んだ比率で
	// 焼かれてしまい、誰も気付かないまま縦横が変わります。
	KeyframeAspectRatio string
	// KeyframeImageSize はキーフレーム画像の出力解像度です（例: "1K", "2K"）。
	// **必須です**（Validate 参照）。
	KeyframeImageSize string
	// KeyframeNegativePrompt はキーフレーム生成で排除したい要素です
	// （例: "speech bubble, text, watermark"）。
	//
	// 必須ではありません。空は「排除指定なし」という意味のある選択だからです。
	// ただしキットは既定文言を持たないので、文字やフキダシを避けたいなら明示して
	// ください。文言をキットに置かないのは、そこに画風の指定（monochrome など）が
	// 混ざると、モノクロで作りたい作品がキットのリリース無しには作れなくなるためです。
	KeyframeNegativePrompt string
}

// ApplyDefaults は未設定（ゼロ値）の項目にデフォルト値を適用します。
//
// RateInterval は補完しません。0 は「制限なし」という意味を持つ値で、既定値を
// 差し込むと呼び出し側が明示的に無制限を選べなくなるためです。
func (c *Config) ApplyDefaults() {
	if c.MaxConcurrency <= 0 {
		c.MaxConcurrency = DefaultMaxConcurrency
	}
	if c.RequestTimeout <= 0 {
		c.RequestTimeout = DefaultRequestTimeout
	}
}

// Validate はモデル名が指定されていることを検証します。
func (c *Config) Validate() error {
	missing := make([]string, 0, 4)
	if strings.TrimSpace(c.GeminiModel) == "" {
		missing = append(missing, "GeminiModel")
	}
	if strings.TrimSpace(c.ImageModel) == "" {
		missing = append(missing, "ImageModel")
	}
	if strings.TrimSpace(c.KeyframeAspectRatio) == "" {
		missing = append(missing, "KeyframeAspectRatio")
	}
	if strings.TrimSpace(c.KeyframeImageSize) == "" {
		missing = append(missing, "KeyframeImageSize")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: %s must be set (this library holds no default model names and no art direction)", ErrConfigInvalid, strings.Join(missing, ", "))
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
