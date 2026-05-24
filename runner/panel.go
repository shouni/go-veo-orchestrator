package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-veo-orchestrator/asset"
	"github.com/shouni/go-veo-orchestrator/ports"
)

const defaultCacheControl = "public, max-age=1800"

// CutKeyframeRunner は、動画台本を元に並列キーフレーム生成を管理します。
type CutKeyframeRunner struct {
	generator ports.PanelsImageGenerator
	writer    remoteio.Writer
}

// NewCutKeyframeRunner は、依存関係を注入して初期化します。
func NewCutKeyframeRunner(
	generator ports.PanelsImageGenerator,
	writer remoteio.Writer,
) *CutKeyframeRunner {
	return &CutKeyframeRunner{
		generator: generator,
		writer:    writer,
	}
}

// NewMangaPanelRunner は旧 API 互換のコンストラクタです。
func NewMangaPanelRunner(
	generator ports.PanelsImageGenerator,
	writer remoteio.Writer,
) *CutKeyframeRunner {
	return NewCutKeyframeRunner(generator, writer)
}

// Run は、台本(VideoRecipe)を受け取り、カットのキーフレーム画像を生成します。
func (r *CutKeyframeRunner) Run(ctx context.Context, manga *ports.MangaResponse) ([]*imagePorts.ImageResponse, error) {
	if manga == nil {
		return nil, fmt.Errorf("VideoRecipe がありません")
	}
	manga.Normalize()

	slog.Info("Starting parallel cut keyframe generation")

	images, err := r.generator.Execute(ctx, manga.Panels)
	if err != nil {
		slog.Error("Image generation pipeline failed", "error", err)
		return nil, err
	}

	slog.Info("Successfully generated cut keyframes", "count", len(images))
	return images, nil
}

// RunAndSave はカットキーフレームを生成し、インデックスを付けて指定のパスに保存します。
func (r *CutKeyframeRunner) RunAndSave(ctx context.Context, manga *ports.MangaResponse, outputPath string) (*ports.MangaResponse, error) {
	if manga == nil {
		return nil, fmt.Errorf("VideoRecipe がありません")
	}
	manga.Normalize()

	// 保存先ディレクトリの決定
	targetDir := asset.ResolveBaseURL(outputPath)

	// ベースとなる出力パスを解決します（GCS/ローカルを判別し、ベースファイル名を結合）
	basePath, err := asset.ResolveOutputPath(targetDir, asset.DefaultPanelImagePath())
	if err != nil {
		return nil, fmt.Errorf("出力パスの解決に失敗しました: %w", err)
	}

	// 画像の生成
	images, err := r.Run(ctx, manga)
	if err != nil {
		return nil, err // Run 内部でエラーラップされているためそのまま返す
	}

	if len(images) != len(manga.Panels) {
		return nil, fmt.Errorf("生成された画像の数(%d)とパネルの数(%d)が一致しません", len(images), len(manga.Panels))
	}
	for i, image := range images {
		// 連番を付けて保存
		panelPath, err := asset.GenerateIndexedPath(basePath, i+1)
		if err != nil {
			return nil, fmt.Errorf("パネル %d の出力パス生成に失敗しました: %w", i+1, err)
		}

		slog.InfoContext(ctx, "パネル画像を保存しています",
			"index", i+1,
			"path", panelPath,
		)

		if err := r.writer.Write(ctx, panelPath, bytes.NewReader(image.Data),
			remoteio.WithContentType(image.MimeType),
			remoteio.WithCacheControl(defaultCacheControl),
		); err != nil {
			// エラー発生時は、それまでの成果物は返さず、nilとエラーを返す
			return nil, fmt.Errorf("第 %d パネルの保存に失敗しました (path: %s): %w", i+1, panelPath, err)
		}
		manga.Panels[i].ReferenceURL = panelPath
	}

	plotPath, err := asset.ResolveOutputPath(targetDir, asset.DefaultVideoRecipeJSON)
	if err != nil {
		return nil, fmt.Errorf("プロットファイル出力パスの解決に失敗しました: %w", err)
	}

	// JSONにシリアライズして保存
	plotData, err := json.MarshalIndent(manga, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("台本データのJSON変換に失敗しました: %w", err)
	}

	slog.InfoContext(ctx, "更新された台本を保存しています", "path", plotPath)
	if err := r.writer.Write(ctx, plotPath, bytes.NewReader(plotData),
		remoteio.WithContentType("application/json"),
		remoteio.WithCacheControl(defaultCacheControl),
	); err != nil {
		return nil, fmt.Errorf("プロットファイルの保存に失敗しました: %w", err)
	}

	return manga, nil
}

// MangaPanelRunner は旧 API 互換のエイリアスです。
type MangaPanelRunner = CutKeyframeRunner
