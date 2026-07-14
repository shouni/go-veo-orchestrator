// Package runner は、動画レシピの実行（キーフレーム生成・スクリプト実行・
// 動画生成・公開）を統括するランナー群を提供します。
package runner

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-veo-orchestrator/ports"
)

const defaultCacheControl = "public, max-age=1800"

// CutKeyframeRunner は、動画レシピを元に並列キーフレーム生成を管理します。
type CutKeyframeRunner struct {
	generator ports.CutImageGenerator
	writer    remoteio.Writer
}

// NewCutKeyframeRunner は、依存関係を注入して初期化します。
func NewCutKeyframeRunner(
	generator ports.CutImageGenerator,
	writer remoteio.Writer,
) *CutKeyframeRunner {
	return &CutKeyframeRunner{
		generator: generator,
		writer:    writer,
	}
}

// Run は、動画レシピを受け取り、カットのキーフレーム画像を生成します。
func (r *CutKeyframeRunner) Run(ctx context.Context, recipe *ports.VideoRecipe) ([]*imagePorts.ImageResponse, error) {
	if recipe == nil {
		return nil, ports.ErrRecipeRequired
	}
	recipe.Normalize()

	slog.Info("Starting parallel cut keyframe generation")

	images, err := r.generator.Execute(ctx, recipe.Cuts)
	if err != nil {
		return nil, fmt.Errorf("cut keyframe generation failed: %w", err)
	}

	slog.Info("Successfully generated cut keyframes", "count", len(images))
	return images, nil
}

// RunAndSave はカットキーフレームを生成し、インデックスを付けて指定のパスに保存します。
func (r *CutKeyframeRunner) RunAndSave(ctx context.Context, recipe *ports.VideoRecipe, outputPath string) (*ports.VideoRecipe, error) {
	if recipe == nil {
		return nil, ports.ErrRecipeRequired
	}
	recipe.Normalize()

	targetDir := resolveBaseURL(outputPath)
	basePath, err := resolveOutputPath(targetDir, defaultKeyframePath())
	if err != nil {
		return nil, fmt.Errorf("出力パスの解決に失敗しました: %w", err)
	}

	images, err := r.Run(ctx, recipe)
	if err != nil {
		return nil, err
	}

	if len(images) != len(recipe.Cuts) {
		return nil, fmt.Errorf("生成された画像の数(%d)とカット数(%d)が一致しません", len(images), len(recipe.Cuts))
	}
	for i, image := range images {
		keyframePath, err := generateIndexedPath(basePath, i+1)
		if err != nil {
			return nil, fmt.Errorf("cut %d のキーフレーム出力パス生成に失敗しました: %w", i+1, err)
		}

		slog.InfoContext(ctx, "キーフレーム画像を保存しています",
			"index", i+1,
			"path", keyframePath,
		)

		if err := r.writer.Write(ctx, keyframePath, bytes.NewReader(image.Data),
			remoteio.WithContentType(image.MimeType),
			remoteio.WithCacheControl(defaultCacheControl),
		); err != nil {
			return nil, fmt.Errorf("cut %d のキーフレーム保存に失敗しました (path: %s): %w", i+1, keyframePath, err)
		}
		recipe.Cuts[i].KeyframeReference = keyframePath
	}

	slog.InfoContext(ctx, "更新された動画メタデータを保存しています", "output_dir", targetDir)
	if _, err := writeRecipeMetadata(ctx, r.writer, targetDir, recipe); err != nil {
		return nil, err
	}

	return recipe, nil
}

// cutImageEditor is implemented by image generators that can edit an existing single-cut
// keyframe image using a text instruction, instead of regenerating it from scratch.
type cutImageEditor interface {
	EditCut(ctx context.Context, cut ports.Cut, editPrompt string) (*imagePorts.ImageResponse, error)
}

// EditAndSave edits the existing keyframe image of a single-cut recipe using editPrompt
// (preserving composition/pose rather than regenerating from scratch), saves the result the
// same way RunAndSave does, and returns the recipe with the updated KeyframeReference.
func (r *CutKeyframeRunner) EditAndSave(ctx context.Context, recipe *ports.VideoRecipe, editPrompt string, outputPath string) (*ports.VideoRecipe, error) {
	if recipe == nil {
		return nil, ports.ErrRecipeRequired
	}
	recipe.Normalize()
	if len(recipe.Cuts) != 1 {
		return nil, fmt.Errorf("EditAndSave は単一カットの recipe のみ対応しています（cuts=%d）", len(recipe.Cuts))
	}

	editor, ok := r.generator.(cutImageEditor)
	if !ok {
		return nil, ports.ErrEditingNotSupported
	}

	cut := recipe.Cuts[0]
	if cut.KeyframeReference == "" {
		return nil, fmt.Errorf("cut %d に編集対象のキーフレームがありません", cut.CutIndex)
	}

	targetDir := resolveBaseURL(outputPath)
	basePath, err := resolveOutputPath(targetDir, defaultKeyframePath())
	if err != nil {
		return nil, fmt.Errorf("出力パスの解決に失敗しました: %w", err)
	}

	slog.InfoContext(ctx, "キーフレームを編集しています", "cut_index", cut.CutIndex)
	image, err := editor.EditCut(ctx, cut, editPrompt)
	if err != nil {
		return nil, fmt.Errorf("cut %d keyframe edit failed: %w", cut.CutIndex, err)
	}

	keyframePath, err := generateIndexedPath(basePath, 1)
	if err != nil {
		return nil, fmt.Errorf("cut %d のキーフレーム出力パス生成に失敗しました: %w", cut.CutIndex, err)
	}

	slog.InfoContext(ctx, "編集済みキーフレーム画像を保存しています",
		"cut_index", cut.CutIndex,
		"path", keyframePath,
	)
	if err := r.writer.Write(ctx, keyframePath, bytes.NewReader(image.Data),
		remoteio.WithContentType(image.MimeType),
		remoteio.WithCacheControl(defaultCacheControl),
	); err != nil {
		return nil, fmt.Errorf("cut %d のキーフレーム保存に失敗しました (path: %s): %w", cut.CutIndex, keyframePath, err)
	}
	recipe.Cuts[0].KeyframeReference = keyframePath

	slog.InfoContext(ctx, "更新された動画メタデータを保存しています", "output_dir", targetDir)
	if _, err := writeRecipeMetadata(ctx, r.writer, targetDir, recipe); err != nil {
		return nil, err
	}

	return recipe, nil
}
