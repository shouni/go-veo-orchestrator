package runner

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-veo-orchestrator/asset"
	"github.com/shouni/go-veo-orchestrator/ports"
)

// MangaPageRunner は Markdown の解析、複数ページの画像生成、および成果物の保存を管理します。
type MangaPageRunner struct {
	generator ports.PagesImageGenerator
	writer    remoteio.Writer
}

// NewMangaPageRunner は、設定、パーサー、生成エンジン、およびライターを依存性として注入し、MangaPageRunner を初期化します。
func NewMangaPageRunner(
	generator ports.PagesImageGenerator,
	writer remoteio.Writer,
) *MangaPageRunner {
	return &MangaPageRunner{
		generator: generator,
		writer:    writer,
	}
}

// Run は、構造化された台本データを基に、最終的な漫画ページ画像を生成します。
func (r *MangaPageRunner) Run(ctx context.Context, manga *ports.MangaResponse) ([]*imagePorts.ImageResponse, error) {
	// 1. バリデーション
	if manga == nil {
		return nil, fmt.Errorf("manga データが nil です")
	}

	// ページ生成は Panels を入力として扱います。
	if len(manga.Panels) == 0 {
		return nil, fmt.Errorf("プロットにページデータが含まれていません")
	}

	slog.InfoContext(ctx, "MangaPageRunner: ページ生成を開始します",
		"title", manga.Title,
		"pageCount", len(manga.Panels),
	)

	// 2. ページ生成エンジンを実行
	images, err := r.generator.Execute(ctx, manga)
	if err != nil {
		return nil, fmt.Errorf("ページ画像の生成に失敗しました: %w", err)
	}

	return images, nil
}

// RunAndSave は、画像の生成から指定ディレクトリへの保存までを一括で行います。
func (r *MangaPageRunner) RunAndSave(ctx context.Context, manga *ports.MangaResponse, outputPath string) ([]string, error) {
	if manga == nil {
		return nil, fmt.Errorf("manga データがありません")
	}

	// 1. 保存先ディレクトリの決定
	targetDir := asset.ResolveBaseURL(outputPath)
	if targetDir == "" {
		return nil, fmt.Errorf("アセットパスからベースURLを解決できませんでした: %s", outputPath)
	}

	// 2. ベースとなる出力パスを解決します（GCS/ローカルを判別し、ベースファイル名を結合）
	basePath, err := asset.ResolveOutputPath(targetDir, asset.DefaultPageImagePath())
	if err != nil {
		return nil, fmt.Errorf("出力パスの解決に失敗しました: %w", err)
	}

	// 3. 画像の生成
	responses, err := r.Run(ctx, manga)
	if err != nil {
		return nil, err
	}

	// 4. 連番を付けて保存
	return r.savePages(ctx, responses, basePath)
}

// savePages は、一連の画像応答を、ファイル名に連番を付けて保存します。
func (r *MangaPageRunner) savePages(ctx context.Context, responses []*imagePorts.ImageResponse, basePath string) ([]string, error) {
	var savedPaths []string
	for i, resp := range responses {
		// 例: manga_page.png -> manga_page_1.png
		pagePath, err := asset.GenerateIndexedPath(basePath, i+1)
		if err != nil {
			return nil, fmt.Errorf("ページ %d の出力パス生成に失敗しました: %w", i+1, err)
		}

		slog.InfoContext(ctx, "ページ画像を保存しています",
			"index", i+1,
			"path", pagePath,
		)

		if err = r.writer.Write(ctx, pagePath, bytes.NewReader(resp.Data),
			remoteio.WithContentType(resp.MimeType),
			remoteio.WithCacheControl(defaultCacheControl),
		); err != nil {
			return nil, fmt.Errorf("第 %d ページの保存に失敗しました (path: %s): %w", i+1, pagePath, err)
		}
		savedPaths = append(savedPaths, pagePath)
	}

	return savedPaths, nil
}
