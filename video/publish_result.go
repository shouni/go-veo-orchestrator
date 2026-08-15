package video

// PublishResult はパブリッシュ処理の結果として生成されたファイルの情報を保持します。
type PublishResult struct {
	MetadataPath string   // 生成された video_music_meta.json のパス
	ImagePaths   []string // 保存された全キーフレーム画像のパスリスト
}
