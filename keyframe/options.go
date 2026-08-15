package keyframe

// Option は Generator の設定を適用する関数型です。
//
// 発射間隔とリクエストタイムアウトのオプションはここにはありません。それらは
// 注入側（workflow の callGuard）が受け持ちます — 同じリミッターを台本のテキスト
// 生成にも掛ける必要があり、クォータはプロジェクト単位だからです。
type Option func(*Generator)

func applyDefaultOptions(g *Generator) {
	g.aspectRatio = CutAspectRatio
	g.imageSize = ImageSize2K
	g.negativePrompt = defaultNegativeKeyframePrompt
	g.maxConcurrency = 1
}

// WithMaxConcurrency は、カット間のキーフレーム生成の同時実行数を設定します。
// 1 以下（既定は 1）なら逐次実行です。
//
// 注意: 注入側にレート制限が掛かっている場合、スループットは並列度によらず
// 発射間隔で頭打ちになります。両方を大きくする設定は矛盾しています。
func WithMaxConcurrency(n int) Option {
	return func(g *Generator) {
		if n > 0 {
			g.maxConcurrency = n
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

// WithImageSize は、生成するキーフレーム画像の解像度を設定します（例: "1K", "2K"）。
// 空文字の場合は既定値（ImageSize2K）のまま変更しません。
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
