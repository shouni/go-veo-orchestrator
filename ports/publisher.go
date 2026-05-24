package ports

// PublishOptions はパブリッシュ動作および Markdown 構築を制御する設定項目です。
type PublishOptions struct {
	OutputDir  string
	ImagePaths []string // 明示的に画像パスを指定する場合に使用。空なら ReferenceURL を使用します。
}

// PublishResult はパブリッシュ処理の結果として生成されたファイルの情報を保持します。
type PublishResult struct {
	MarkdownPath string   // 生成された manga_plot.md のパス
	HTMLPath     string   // 生成された HTML のパス
	ImagePaths   []string // 保存された全画像のパスリスト
}
