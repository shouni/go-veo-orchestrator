package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	"github.com/shouni/go-gemini-client/callguard"
	"github.com/shouni/go-gemini-client/gemini"
)

// 本ファイルは、台本のテキスト生成とキーフレームの画像生成に呼び出しガードを被せる
// デコレータと、リクエスト内容からキーを作る部分を持ちます。発射間隔・上限時間・
// 同時実行の重複排除そのものは callguard が持っており、go-comic-kit や
// go-gemini-client/lyria と同じ実装を共有します。
//
// ガード（callguard.Guard）はワークフロー全体で 1 つを共有し、テキスト生成にも画像生成にも
// 同じものを掛けます。クォータはプロジェクト単位で操作の種類ごとではないため、片方だけ
// 絞っても意味がないからです。これが、発射間隔と上限時間を画像キットのオプション
// （WithRateLimit / WithRequestTimeout）ではなくここに置いている理由でもあります。

// singleflightImageGenerator は、同一内容の画像生成リクエストの同時実行を1回にまとめる
// デコレータです。Cloud Tasks の at-least-once 配信やリトライによる重複呼び出しから、
// 高価な画像生成 API 呼び出しを守ります。プロセス内の in-flight のみが対象で、
// 恒久的な重複排除は recipe の keyframe_reference によるジョブ側の冪等性で行います。
type singleflightImageGenerator struct {
	inner imagePorts.ImageGenerator
	guard *callguard.Guard
	group callguard.Group
}

var _ imagePorts.ImageGenerator = (*singleflightImageGenerator)(nil)

// Generate はリクエスト内容のハッシュをキーに同時実行をまとめます。
func (g *singleflightImageGenerator) Generate(ctx context.Context, req imagePorts.ImageRequest) (*imagePorts.ImageResponse, error) {
	key := imageRequestKey(&req)
	resp, err := callguard.Do(ctx, &g.group, g.guard, key, func(execCtx context.Context) (*imagePorts.ImageResponse, error) {
		return g.inner.Generate(execCtx, req)
	})
	if err != nil {
		return nil, err
	}
	return cloneImageResponse(resp), nil
}

// singleflightGenerator は、同一内容のテキスト生成リクエストの同時実行を
// 1回にまとめる gemini.Generator のデコレータです。
type singleflightGenerator struct {
	inner gemini.Generator
	guard *callguard.Guard
	group callguard.Group
}

var _ gemini.Generator = (*singleflightGenerator)(nil)

// GenerateWithAttachments はリクエスト内容のハッシュをキーに同時実行をまとめます。
func (g *singleflightGenerator) GenerateWithAttachments(ctx context.Context, modelName string, prompt string, attachments []gemini.Attachment, opts gemini.GenerateOptions) (*gemini.Response, error) {
	key := textRequestKey(modelName, prompt, attachments, &opts)
	resp, err := callguard.Do(ctx, &g.group, g.guard, key, func(execCtx context.Context) (*gemini.Response, error) {
		return g.inner.GenerateWithAttachments(execCtx, modelName, prompt, attachments, opts)
	})
	if err != nil {
		return nil, err
	}
	// NOTE: 浅いコピーで返します。呼び出し側（runner）は Text しか参照しない前提です。
	// gemini.Response の参照型フィールドを書き換える利用が増えた場合は深いコピーに変更すること。
	cloned := *resp
	return &cloned, nil
}

// imageRequestKey は画像生成リクエストの内容から singleflight 用キーを作ります。
//
// go-comic-kit の同名関数は ImageURI.FileAPIURI もキーに含めますが、こちらは含めません。
// このキットの参照解決は GCSResolver 単体（gs:// をそのまま Vertex AI へ渡す）で、
// File API へ上げる経路が無く、FileAPIURI が常に空だからです。
func imageRequestKey(req *imagePorts.ImageRequest) string {
	parts := []string{
		req.Model,
		req.Prompt,
		req.SystemPrompt,
		req.NegativePrompt,
		req.AspectRatio,
		req.ImageSize,
		callguard.SeedKey(req.Seed),
	}
	for _, img := range req.Images {
		parts = append(parts, img.ReferenceURL)
	}
	return callguard.Key("image", parts...)
}

// textRequestKey はテキスト生成リクエストの内容から singleflight 用キーを作ります。
//
// 添付は URI かバイト列のどちらかなので、URI はそのまま、バイト列は中身のハッシュを
// キーに含めます。長さだけで代用すると、同じサイズの別画像が同じキーになります。
func textRequestKey(modelName string, prompt string, attachments []gemini.Attachment, opts *gemini.GenerateOptions) string {
	keyParts := []string{modelName, opts.ResponseMIMEType, callguard.SeedKey(opts.Seed), prompt}
	for _, attachment := range attachments {
		keyParts = append(keyParts, attachment.MIMEType, attachment.URI)
		if len(attachment.Data) > 0 {
			sum := sha256.Sum256(attachment.Data)
			keyParts = append(keyParts, hex.EncodeToString(sum[:]))
		}
	}
	return callguard.Key("text", keyParts...)
}

// cloneImageResponse は singleflight で共有される応答を呼び出し元が安全に扱えるよう複製します。
func cloneImageResponse(src *imagePorts.ImageResponse) *imagePorts.ImageResponse {
	if src == nil {
		return nil
	}
	dst := *src
	dst.Data = append([]byte(nil), src.Data...)
	return &dst
}
