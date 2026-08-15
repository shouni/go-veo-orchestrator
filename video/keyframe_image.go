package video

// KeyframeImage は、生成されたキーフレーム画像 1 枚とそのメタデータです。
//
// vertex-image-kit の ImageResponse をそのまま公開 API に晒すと、このライブラリの
// 利用側（Workflows の消費者やテストのフェイク）が画像キットまで輸入させられます。
// 実際に ap-mv が image-kit へ直接依存していた理由はこのリークだけだったため、
// ライブラリ自身の型として持ちます。
type KeyframeImage struct {
	// Data は画像のバイト列です。
	Data []byte
	// MimeType は画像の MIME type です（保存時の拡張子と Content-Type の決定に使います）。
	MimeType string
	// UsedSeed は生成に使われたシードです。vertex-image-kit は既定でシードを
	// 自動採番するため、通常は実際に送信された値が入ります。
	UsedSeed int64
	// Model は生成に使ったモデル名です。
	Model string
	// Prompt は実際に送信した最終プロンプトです。
	Prompt string
}
