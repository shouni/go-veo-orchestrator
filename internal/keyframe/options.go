package keyframe

// Option は Generator の設定を適用する関数型です。
//
// 画作りの既定値はありません。アスペクト比・解像度・ネガティブプロンプトはすべて
// 呼び出し側（ports.Config 経由）が決めます — 理由は ports.Config の Art Direction を
// 参照してください。並列度・発射間隔・上限時間のオプションもここにはありません
// （runner.CutKeyframeRunner と workflow の callGuard が持ちます）。
type Option func(*Generator)

// WithAspectRatio は、生成するキーフレーム画像のアスペクト比を設定します
// （例: "16:9", "9:16"）。空文字の場合は何も変更しません。未設定ならモデルへ空が渡りますが、
// ports.Config.KeyframeAspectRatio が必須なので workflow 経由で組み立てる限り空にはなりません。
func WithAspectRatio(value string) Option {
	return func(g *Generator) {
		if value != "" {
			g.aspectRatio = value
		}
	}
}

// WithImageSize は、生成するキーフレーム画像の解像度を設定します（例: "1K", "2K"）。
// 空文字の場合は何も変更しません。
func WithImageSize(value string) Option {
	return func(g *Generator) {
		if value != "" {
			g.imageSize = value
		}
	}
}

// WithNegativePrompt は、キーフレーム生成のネガティブプロンプトを設定します。
// 既定文言はありません（理由は ports.Config.KeyframeNegativePrompt を参照）。
func WithNegativePrompt(value string) Option {
	return func(g *Generator) {
		g.negativePrompt = value
	}
}
