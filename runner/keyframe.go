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

// mustMatchCutCount は、カット単位で生成した成果物（キーフレーム画像など）の数 got が
// カット数 want と一致することを検証します。一致すれば nil を返します。kind は成果物の
// 呼称（例: "生成された画像の数" / "生成されたキーフレーム数"）で、エラーメッセージの
// 文言をそれぞれの呼び出し元と同一に保つためのラベルです。
func mustMatchCutCount(kind string, got, want int) error {
	if got == want {
		return nil
	}
	return fmt.Errorf("%s(%d)とカット数(%d)が一致しません", kind, got, want)
}

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

	targetDir, basePath, err := resolveKeyframeBasePath(outputPath)
	if err != nil {
		return nil, err
	}

	images, err := r.Run(ctx, recipe)
	if err != nil {
		return nil, err
	}

	if err := mustMatchCutCount("生成された画像の数", len(images), len(recipe.Cuts)); err != nil {
		return nil, err
	}
	for i, image := range images {
		keyframePath, err := r.saveKeyframeImage(ctx, basePath, i+1, image)
		if err != nil {
			return nil, err
		}
		recipe.Cuts[i].KeyframeReference = keyframePath
	}

	slog.InfoContext(ctx, "更新された動画メタデータを保存しています", "output_dir", targetDir)
	if _, err := writeRecipeMetadata(ctx, r.writer, targetDir, recipe); err != nil {
		return nil, err
	}

	return recipe, nil
}

// resolveKeyframeBasePath は、RunAndSave / EditAndSave が共通して必要とする保存先ディレクトリと
// インデックス付きキーフレームパスの基点を解決します。
func resolveKeyframeBasePath(outputPath string) (targetDir string, basePath string, err error) {
	targetDir = resolveBaseURL(outputPath)
	basePath, err = resolveOutputPath(targetDir, defaultKeyframePath())
	if err != nil {
		return "", "", fmt.Errorf("出力パスの解決に失敗しました: %w", err)
	}
	return targetDir, basePath, nil
}

// saveKeyframeImage は、basePath から index 番目のキーフレームパスを生成し、画像を保存します。
// RunAndSave / EditAndSave の両方から使われる共通の保存ロジックです。
func (r *CutKeyframeRunner) saveKeyframeImage(ctx context.Context, basePath string, index int, image *imagePorts.ImageResponse) (string, error) {
	keyframePath, err := generateIndexedPath(basePath, index)
	if err != nil {
		return "", fmt.Errorf("cut %d のキーフレーム出力パス生成に失敗しました: %w", index, err)
	}

	slog.InfoContext(ctx, "キーフレーム画像を保存しています",
		"index", index,
		"path", keyframePath,
	)

	if err := r.writer.Write(ctx, keyframePath, bytes.NewReader(image.Data),
		remoteio.WithContentType(image.MimeType),
		remoteio.WithCacheControl(defaultCacheControl),
	); err != nil {
		return "", fmt.Errorf("cut %d のキーフレーム保存に失敗しました (path: %s): %w", index, keyframePath, err)
	}

	return keyframePath, nil
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
		return nil, fmt.Errorf("%w（cuts=%d）", ports.ErrSingleCutRequired, len(recipe.Cuts))
	}

	editor, ok := r.generator.(cutImageEditor)
	if !ok {
		return nil, ports.ErrEditingNotSupported
	}

	cut := recipe.Cuts[0]
	if cut.KeyframeReference == "" {
		return nil, fmt.Errorf("cut %d: %w", cut.CutIndex, ports.ErrNoKeyframeToEdit)
	}

	targetDir, basePath, err := resolveKeyframeBasePath(outputPath)
	if err != nil {
		return nil, err
	}

	slog.InfoContext(ctx, "キーフレームを編集しています", "cut_index", cut.CutIndex)
	image, err := editor.EditCut(ctx, cut, editPrompt)
	if err != nil {
		return nil, fmt.Errorf("cut %d keyframe edit failed: %w", cut.CutIndex, err)
	}

	keyframePath, err := r.saveKeyframeImage(ctx, basePath, 1, image)
	if err != nil {
		return nil, err
	}
	recipe.Cuts[0].KeyframeReference = keyframePath

	slog.InfoContext(ctx, "更新された動画メタデータを保存しています", "output_dir", targetDir)
	if _, err := writeRecipeMetadata(ctx, r.writer, targetDir, recipe); err != nil {
		return nil, err
	}

	return recipe, nil
}
