package runner

import (
	"context"

	"github.com/shouni/go-veo-orchestrator/ports"
	"github.com/shouni/go-veo-orchestrator/publisher"
)

// MangaPublisherRunner は pkg/publisher を利用して漫画成果物の公開と構築を担います。
type MangaPublisherRunner struct {
	publisher *publisher.MangaPublisher
}

// NewMangaPublisherRunner は、指定された構成と MangaPublisher を持つ新しい MangaPublisherRunner インスタンスを作成します。
func NewMangaPublisherRunner(pub *publisher.MangaPublisher) *MangaPublisherRunner {
	return &MangaPublisherRunner{
		publisher: pub,
	}
}

// Run は漫画データの公開処理を実行し、Markdown や HTML などの成果物を指定された出力ディレクトリに保存します。
func (pr *MangaPublisherRunner) Run(ctx context.Context, manga *ports.MangaResponse, outputDir string) (*ports.PublishResult, error) {
	opts := ports.PublishOptions{
		OutputDir: outputDir,
	}

	return pr.publisher.Publish(ctx, manga, opts)
}

// BuildMarkdown は保存処理を行わず、構造体から Markdown 文字列のみを生成して返却します。
func (pr *MangaPublisherRunner) BuildMarkdown(manga *ports.MangaResponse) string {
	// publisher.Options を空で渡すことで、外部パス指定を行わず、
	// domain.MangaResponse 内の ReferenceURL をそのまま使用するデフォルト挙動を選択します。
	return pr.publisher.BuildMarkdown(manga, ports.PublishOptions{})
}
