package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	"github.com/shouni/go-gemini-client/gemini"
	"golang.org/x/sync/singleflight"
)

// callGuard は、singleflight デコレータが共有する呼び出しガードです。
// AI API を叩く実行そのものに、発射間隔（Config.RateInterval）と1回あたりの
// 上限時間（Config.RequestTimeout）を適用します。
//
// ワークフロー全体で 1 つのインスタンスを共有し、台本のテキスト生成とキーフレームの
// 画像生成の両方に同じガードを掛けます。クォータはプロジェクト単位で操作の種類ごとでは
// ないため、片方だけ絞っても意味がないからです。これが、発射間隔と上限時間を
// 画像キットのオプション（WithRateLimit / WithRequestTimeout）ではなく
// ここに置いている理由でもあります。
type callGuard struct {
	// limiter は発射間隔のリミッターです。nil は制限なしを意味します。
	limiter *rateLimiter
	// timeout は1回の呼び出しの上限時間です。0 以下は無制限を意味します。
	timeout time.Duration
}

// singleflightImageGenerator は、同一内容の画像生成リクエストの同時実行を1回にまとめる
// デコレータです。Cloud Tasks の at-least-once 配信やリトライによる重複呼び出しから、
// 高価な画像生成 API 呼び出しを守ります。プロセス内の in-flight のみが対象で、
// 恒久的な重複排除は recipe の keyframe_reference によるジョブ側の冪等性で行います。
type singleflightImageGenerator struct {
	inner imagePorts.ImageGenerator
	guard callGuard
	group singleflight.Group
}

var _ imagePorts.ImageGenerator = (*singleflightImageGenerator)(nil)

// Generate はリクエスト内容のハッシュをキーに同時実行をまとめます。
func (g *singleflightImageGenerator) Generate(ctx context.Context, req imagePorts.ImageRequest) (*imagePorts.ImageResponse, error) {
	key := imageRequestKey(&req)
	resp, err := doSingleflight(ctx, &g.group, g.guard, key, func(execCtx context.Context) (*imagePorts.ImageResponse, error) {
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
	guard callGuard
	group singleflight.Group
}

var _ gemini.Generator = (*singleflightGenerator)(nil)

// GenerateWithAttachments はリクエスト内容のハッシュをキーに同時実行をまとめます。
func (g *singleflightGenerator) GenerateWithAttachments(ctx context.Context, modelName string, prompt string, attachments []gemini.Attachment, opts gemini.GenerateOptions) (*gemini.Response, error) {
	key := textRequestKey(modelName, prompt, attachments, &opts)
	resp, err := doSingleflight(ctx, &g.group, g.guard, key, func(execCtx context.Context) (*gemini.Response, error) {
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
func imageRequestKey(req *imagePorts.ImageRequest) string {
	parts := []string{
		req.Model,
		req.Prompt,
		req.SystemPrompt,
		req.NegativePrompt,
		req.AspectRatio,
		req.ImageSize,
		singleflightSeedKey(req.Seed),
	}
	for _, img := range req.Images {
		parts = append(parts, img.ReferenceURL)
	}
	return singleflightKey("image", parts...)
}

// textRequestKey はテキスト生成リクエストの内容から singleflight 用キーを作ります。
//
// 添付は URI かバイト列のどちらかなので、URI はそのまま、バイト列は中身のハッシュを
// キーに含めます。長さだけで代用すると、同じサイズの別画像が同じキーになります。
func textRequestKey(modelName string, prompt string, attachments []gemini.Attachment, opts *gemini.GenerateOptions) string {
	keyParts := []string{modelName, opts.ResponseMIMEType, singleflightSeedKey(opts.Seed), prompt}
	for _, attachment := range attachments {
		keyParts = append(keyParts, attachment.MIMEType, attachment.URI)
		if len(attachment.Data) > 0 {
			sum := sha256.Sum256(attachment.Data)
			keyParts = append(keyParts, hex.EncodeToString(sum[:]))
		}
	}
	return singleflightKey("text", keyParts...)
}

// singleflightKey は namespace と可変長の部品から衝突しにくい singleflight 用キーを作ります。
// 各部品を長さプレフィックス付きでハッシュするため、部品の境界の曖昧さがありません。
func singleflightKey(namespace string, parts ...string) string {
	hasher := sha256.New()
	for _, part := range parts {
		hasher.Write([]byte(strconv.Itoa(len(part))))
		hasher.Write([]byte{0})
		hasher.Write([]byte(part))
		hasher.Write([]byte{0})
	}

	return namespace + ":" + hex.EncodeToString(hasher.Sum(nil))
}

// singleflightSeedKey は nil と実値を区別できる seed 用キー部品を作ります。
func singleflightSeedKey(seed *int64) string {
	if seed == nil {
		return "seed:nil"
	}
	return "seed:" + strconv.FormatInt(*seed, 10)
}

// doSingleflight は同じ key の同時実行をまとめ、呼び出し元のキャンセルも尊重します。
// 実行用 context は共有実行のクロージャ内で呼び出し元から切り離して（WithoutCancel）
// 生成するため、どの呼び出し元がキャンセルしても、相乗りしている他の呼び出し元が
// 巻き添えになりません（実行の打ち切りは guard.timeout でのみ行われます）。
//
// 発射間隔の待機は guard.timeout の外側で行います。待たされた時間を1回あたりの
// 上限時間に数えると、混雑しているだけでタイムアウトしてしまうためです。
func doSingleflight[T any](ctx context.Context, group *singleflight.Group, guard callGuard, key string, fn func(execCtx context.Context) (T, error)) (T, error) {
	ch := group.DoChan(key, func() (any, error) {
		baseCtx := context.WithoutCancel(ctx)
		if err := guard.limiter.wait(baseCtx); err != nil {
			return nil, err
		}
		if guard.timeout <= 0 {
			return fn(baseCtx)
		}
		execCtx, cancel := context.WithTimeout(baseCtx, guard.timeout)
		defer cancel()
		return fn(execCtx)
	})

	select {
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	case result := <-ch:
		if result.Err != nil {
			var zero T
			return zero, result.Err
		}

		value, ok := result.Val.(T)
		if !ok {
			var zero T
			return zero, fmt.Errorf("singleflight result type mismatch for key %s", key)
		}
		return value, nil
	}
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
