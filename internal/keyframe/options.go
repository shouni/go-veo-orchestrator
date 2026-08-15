package keyframe

// Option は Generator の設定を適用する関数型です。
//
// 既定値はありません。アスペクト比・解像度・ネガティブプロンプトはすべて
// 呼び出し側（ports.Config 経由）が決めます。キットが画作りの既定を持つと、
// 呼び出し側の設定と二重の出所になって黙って食い違うためです。
//
// 並列度・発射間隔・リクエストタイムアウトのオプションはここにはありません
// （それぞれ runner.CutKeyframeRunner と workflow の callGuard が持ちます）。
type Option func(*Generator)

// WithAspectRatio は、生成するキーフレーム画像のアスペクト比を設定します
// （例: "16:9", "9:16"）。空文字の場合は何も変更しません。
//
// キットは既定値を持たないため、未設定のままだとモデルへ空が渡ります。
// 実際には ports.Config.KeyframeAspectRatio が必須なので、workflow 経由で
// 組み立てる限り空にはなりません。
func WithAspectRatio(value string) Option {
	return func(g *Generator) {
		if value != "" {
			g.aspectRatio = value
		}
	}
}

// WithImageSize は、生成するキーフレーム画像の解像度を設定します（例: "1K", "2K"）。
// 空文字の場合は何も変更しません（WithAspectRatio と同じく、キットは既定値を持ちません）。
func WithImageSize(value string) Option {
	return func(g *Generator) {
		if value != "" {
			g.imageSize = value
		}
	}
}

// WithNegativePrompt は、キーフレーム生成のネガティブプロンプトを設定します。
//
// キットは既定文言を持ちません。文字やフキダシを避けたいなら呼び出し側が明示して
// ください（ports.Config.KeyframeNegativePrompt）。文言をキットに置かないのは、
// そこに画風の指定（monochrome など）が混ざると、モノクロで作りたい作品が
// キットのリリース無しには作れなくなるためです。
func WithNegativePrompt(value string) Option {
	return func(g *Generator) {
		g.negativePrompt = value
	}
}
