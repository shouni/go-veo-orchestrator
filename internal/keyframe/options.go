package keyframe

// Option は Generator の設定を適用する関数型です。
//
// 既定値はありません。アスペクト比・解像度・ネガティブプロンプトはすべて
// 呼び出し側（ports.Config 経由）が決めます。キットが画作りの既定を持つと、
// 呼び出し側の設定と二重の出所になって黙って食い違うためです。
//
// 並列度・発射間隔・リクエストタイムアウトのオプションはここにはありません。
// 並列度は保存を持つ runner.CutKeyframeRunner が、発射間隔と上限時間は workflow の
// callGuard が受け持ちます（後者は台本のテキスト生成にも同じリミッターを掛ける必要が
// あり、クォータはプロジェクト単位のためです）。
type Option func(*Generator)

// WithAspectRatio は、生成するキーフレーム画像のアスペクト比を設定します
// （例: "16:9", "9:16"）。空文字の場合は既定値（ports.DefaultKeyframeAspectRatio）のまま変更しません。
func WithAspectRatio(value string) Option {
	return func(g *Generator) {
		if value != "" {
			g.aspectRatio = value
		}
	}
}

// WithImageSize は、生成するキーフレーム画像の解像度を設定します（例: "1K", "2K"）。
// 空文字の場合は既定値（ports.DefaultKeyframeImageSize）のまま変更しません。
func WithImageSize(value string) Option {
	return func(g *Generator) {
		if value != "" {
			g.imageSize = value
		}
	}
}

// WithNegativePrompt は、キーフレーム生成のネガティブプロンプトを差し替えます。
// 既定値は文字・フキダシ・低品質などを排除する定型文です。空文字を渡すと
// ネガティブプロンプトなしで生成します。
func WithNegativePrompt(value string) Option {
	return func(g *Generator) {
		g.negativePrompt = value
	}
}
