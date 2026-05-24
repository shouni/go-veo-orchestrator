package publisher

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"strings"

	md "github.com/shouni/go-prompt-kit/md/ports"
	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/go-veo-orchestrator/asset"
	"github.com/shouni/go-veo-orchestrator/ports"
)

const (
	mdContentType       = "text/markdown; charset=utf-8"
	htmlContentType     = "text/html; charset=utf-8"
	defaultCacheControl = "public, max-age=1800"
)

// markdownEscaper は Markdown の制御文字と HTML タグ文字を効率的にエスケープするための Replacer です。
var markdownEscaper = strings.NewReplacer(
	"*", "\\*",
	"_", "\\_",
	"[", "\\[",
	"]", "\\]",
	"#", "\\#",
	"`", "\\`",
	"<", "&lt;",
	">", "&gt;",
)

// MangaPublisher は成果物の永続化とフォーマット変換を担います。
type MangaPublisher struct {
	writer remoteio.Writer
	md     md.Runner
}

// NewMangaPublisher は新しいインスタンスを作成します。
func NewMangaPublisher(writer remoteio.Writer, md md.Runner) *MangaPublisher {
	return &MangaPublisher{
		writer: writer,
		md:     md,
	}
}

// Publish はドメインモデルを基に Markdown を構築し、HTML への変換・保存を実行します。
func (p *MangaPublisher) Publish(ctx context.Context, manga *ports.MangaResponse, opts ports.PublishOptions) (*ports.PublishResult, error) {
	if manga == nil {
		return nil, fmt.Errorf("manga データが nil です")
	}

	markdownPath, err := asset.ResolveOutputPath(opts.OutputDir, asset.DefaultMangaPlotName)
	if err != nil {
		return nil, fmt.Errorf("Markdown 出力パスの解決に失敗: %w", err)
	}

	// 保存用に相対パスリストを作成し、opts にセットする
	imagePaths := make([]string, 0, len(manga.Panels))
	for _, panel := range manga.Panels {
		var relPath string
		if panel.ReferenceURL != "" {
			relPath = path.Join(asset.DefaultImageDir, path.Base(panel.ReferenceURL))
		}
		imagePaths = append(imagePaths, relPath)
	}

	opts.ImagePaths = imagePaths // 構築用にセット
	// 共通の BuildMarkdown を使用
	content := p.BuildMarkdown(manga, opts)

	// Markdown の保存
	slog.InfoContext(ctx, "Markdown ファイルを保存しています", "path", markdownPath)

	// Markdown書き込み
	contentReader := strings.NewReader(content)
	if err := p.writer.Write(ctx, markdownPath, contentReader,
		remoteio.WithContentType(mdContentType),
		remoteio.WithCacheControl(defaultCacheControl),
	); err != nil {
		return nil, fmt.Errorf("Markdown 書き込み失敗: %w", err)
	}

	// HTML の生成
	var htmlPath string
	if p.md != nil {
		htmlBuffer, err := p.md.Run(manga.Title, []byte(content))
		if err != nil {
			return nil, fmt.Errorf("HTML 変換失敗: %w", err)
		}
		htmlPath = strings.TrimSuffix(markdownPath, path.Ext(markdownPath)) + ".html"
		if err := p.writer.Write(ctx, htmlPath, htmlBuffer,
			remoteio.WithContentType(htmlContentType),
			remoteio.WithCacheControl(defaultCacheControl),
		); err != nil {
			return nil, fmt.Errorf("HTML 書き込み失敗: %w", err)
		}
	}

	return &ports.PublishResult{
		MarkdownPath: markdownPath,
		HTMLPath:     htmlPath,
		ImagePaths:   imagePaths,
	}, nil
}

// BuildMarkdown は画像、話者、セリフ、確認用アンカーを含む Markdown を構築します。
func (p *MangaPublisher) BuildMarkdown(manga *ports.MangaResponse, opts ports.PublishOptions) string {
	var sb strings.Builder

	// タイトルと説明文
	sb.WriteString(fmt.Sprintf("# %s\n\n", escapeMarkdown(manga.Title)))
	if manga.Description != "" {
		sb.WriteString(escapeMarkdown(manga.Description) + "\n\n")
	}

	firstPanel := true
	for i, panel := range manga.Panels {
		var currentImagePath string
		// opts.ImagePaths が指定されていればそれを使用し、なければ構造体の ReferenceURL を使用
		if i < len(opts.ImagePaths) && opts.ImagePaths[i] != "" {
			currentImagePath = opts.ImagePaths[i]
		} else {
			currentImagePath = panel.ReferenceURL
		}

		if currentImagePath == "" && panel.Dialogue == "" {
			continue
		}
		if !firstPanel {
			sb.WriteString("---\n\n")
		}
		firstPanel = false

		// 1. 画像
		if currentImagePath != "" {
			sb.WriteString(fmt.Sprintf("![Panel %d](%s)\n\n", i+1, currentImagePath))
		}

		// 2. セリフ
		if panel.Dialogue != "" {
			dialogue := escapeMarkdown(panel.Dialogue)
			if panel.SpeakerID != "" {
				sb.WriteString(fmt.Sprintf("**%s**: %s\n\n", escapeMarkdown(panel.SpeakerID), dialogue))
			} else {
				sb.WriteString(fmt.Sprintf("%s\n\n", dialogue))
			}
		}

		// 3. VisualAnchor
		if panel.VisualAnchor != "" {
			sb.WriteString(fmt.Sprintf("> **Visual Anchor:** %s\n\n", escapeMarkdown(panel.VisualAnchor)))
		}
	}

	return sb.String()
}

// escapeMarkdown は Markdown の制御文字と HTML 特殊文字を安全に置換します。
func escapeMarkdown(text string) string {
	return markdownEscaper.Replace(text)
}
