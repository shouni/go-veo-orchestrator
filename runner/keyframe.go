// Package runner は、動画レシピの実行（キーフレーム生成・スクリプト実行・
// 動画生成・公開）を統括するランナー群を提供します。
package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"strings"

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

// DefaultKeyframeCacheControl は、保存するキーフレーム画像に付ける既定の
// Cache-Control ヘッダです（WithCacheControl で差し替え可能）。
const DefaultKeyframeCacheControl = "public, max-age=1800"

// CutKeyframeRunner は、動画レシピを元にカットキーフレーム生成を管理します。
type CutKeyframeRunner struct {
	generator    ports.CutImageGenerator
	writer       remoteio.Writer
	cacheControl string
}

// KeyframeRunnerOption は CutKeyframeRunner の任意設定です。
type KeyframeRunnerOption func(*CutKeyframeRunner)

// WithCacheControl は、保存するキーフレーム画像に付ける Cache-Control ヘッダを
// 差し替えます。既定は DefaultKeyframeCacheControl（public キャッシュ許可）ですが、
// 生成物を公開したくないデプロイでは "private" 等を指定してください。
func WithCacheControl(value string) KeyframeRunnerOption {
	return func(r *CutKeyframeRunner) {
		if value != "" {
			r.cacheControl = value
		}
	}
}

// NewCutKeyframeRunner は、依存関係を注入して初期化します。
func NewCutKeyframeRunner(
	generator ports.CutImageGenerator,
	writer remoteio.Writer,
	opts ...KeyframeRunnerOption,
) *CutKeyframeRunner {
	r := &CutKeyframeRunner{
		generator:    generator,
		writer:       writer,
		cacheControl: DefaultKeyframeCacheControl,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(r)
		}
	}
	return r
}

// Run は、動画レシピのカットのうち**キーフレーム画像をまだ持たないものだけ**を生成し、
// カット列と同じ長さ・同じ並びのスライスを返します。すでに KeyframeReference を持つ
// カットの位置は nil です（呼び出し側はその参照をそのまま使えばよく、焼き直す理由が
// ないため）。VideoTimelineRunner はこの nil を「既存の参照を使う」として扱います。
//
// 生成が途中で失敗した場合も、成功した位置には結果が入った部分結果を返します
// （エラーと両方を見てください）。判定は RunAndSave と同じ「KeyframeReference が
// 空 = 未生成」です。焼き直したいカットは呼び出し側が参照を空にしてから渡してください。
func (r *CutKeyframeRunner) Run(ctx context.Context, recipe *ports.VideoRecipe) ([]*ports.KeyframeImage, error) {
	if recipe == nil {
		return nil, ports.ErrRecipeRequired
	}
	recipe.Normalize()

	images := make([]*ports.KeyframeImage, len(recipe.Cuts))
	pending := pendingKeyframeCutPositions(recipe.Cuts)
	if len(pending) == 0 {
		slog.InfoContext(ctx, "全カットにキーフレームがあるため生成をスキップしました",
			"cuts", len(recipe.Cuts))
		return images, nil
	}

	targets := make([]ports.Cut, 0, len(pending))
	for _, i := range pending {
		targets = append(targets, recipe.Cuts[i])
	}

	slog.InfoContext(ctx, "Starting cut keyframe generation",
		"cuts", len(targets), "skipped", len(recipe.Cuts)-len(targets))

	generated, genErr := r.generator.Execute(ctx, targets)
	if err := mustMatchCutCount("生成された画像の数", len(generated), len(targets)); err != nil {
		// 部分失敗でも Execute は全長のスライスを返す契約。長さ違いは実装の破れ。
		return nil, errors.Join(err, genErr)
	}
	// 生成対象を絞っても、返すスライスの添字はレシピ内の位置のままにする。詰めると
	// 呼び出し側（VideoTimelineRunner、RunAndSave の保存名）がカットを取り違える。
	for n, image := range generated {
		images[pending[n]] = image
	}
	if genErr != nil {
		return images, fmt.Errorf("cut keyframe generation failed: %w", genErr)
	}

	slog.InfoContext(ctx, "Successfully generated cut keyframes", "count", len(generated))
	return images, nil
}

// RunAndSave はカットキーフレームを生成し、インデックスを付けて指定のパスに保存します。
//
// **すでに KeyframeReference を持つカットは焼き直しません。** レシピを「あるべき状態」
// として扱い、足りないキーフレームだけを補ってメタデータを保存し直します。これは
// VideoTimelineRunner.Run が Cut.IsGenerated() のカットを飛ばすのと同じ考え方で、
// 保存済みレシピを起点に処理を再開しても、すでに払った生成コストを二重に払いません。
// 全カットが揃っていれば画像生成は一度も呼ばれず、メタデータの保存だけを行います。
//
// 生成が途中で失敗した場合も、**成功した分の画像とメタデータは保存してから**エラーを
// 返します。1枚ごとに生成コストが掛かっているため、支払い済みの成果物を捨てず、
// 再実行時は失敗したカットだけを続きから生成できるようにします。
//
// 焼き直したいカットは、呼び出し側が KeyframeReference を空にしてから渡してください
// （「空 = 未生成」がこのメソッドの唯一の判定基準です）。EditAndSave は既存画像を
// 編集ソースにする別経路なので、この判定の対象外です。
func (r *CutKeyframeRunner) RunAndSave(ctx context.Context, recipe *ports.VideoRecipe, outputPath string) (*ports.VideoRecipe, error) {
	if recipe == nil {
		return nil, ports.ErrRecipeRequired
	}
	if r.writer == nil {
		return nil, ports.ErrWriterRequired
	}
	recipe.Normalize()

	targetDir, basePath, err := resolveKeyframeBasePath(outputPath)
	if err != nil {
		return nil, err
	}

	images, genErr := r.Run(ctx, recipe)
	if images == nil && genErr != nil {
		return nil, genErr
	}
	for i, image := range images {
		// nil は「このカットは既存のキーフレームをそのまま使う」（または生成失敗）を意味する。
		if image == nil {
			continue
		}
		keyframePath, err := r.saveKeyframeImage(ctx, basePath, i+1, image)
		if err != nil {
			return nil, errors.Join(err, genErr)
		}
		recipe.Cuts[i].KeyframeReference = keyframePath
	}

	slog.InfoContext(ctx, "更新された動画メタデータを保存しています", "output_dir", targetDir)
	if _, err := writeRecipeMetadata(ctx, r.writer, targetDir, recipe); err != nil {
		return nil, errors.Join(err, genErr)
	}

	if genErr != nil {
		return recipe, genErr
	}
	return recipe, nil
}

// pendingKeyframeCutPositions は、まだキーフレーム画像を持たないカットの位置（0始まり）を
// 返します。判定は KeyframeReference が空かどうかだけで、Cut.IsGenerated()（動画生成の
// 完了）とは別物です。動画が未生成でもキーフレームは焼き済み、という状態が普通にあります。
func pendingKeyframeCutPositions(cuts []ports.Cut) []int {
	var pending []int
	for i := range cuts {
		if strings.TrimSpace(cuts[i].KeyframeReference) == "" {
			pending = append(pending, i)
		}
	}
	return pending
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

// keyframeExtensionForMime は、画像の MIME type から保存ファイルの拡張子を返します。
// 以前は .png 固定で、モデルが JPEG/WebP を返すと中身と拡張子が食い違っていました。
// 未知の MIME type は既定の .png のままにします（Content-Type は別途正しく付くため
// 実害は拡張子の見た目だけ）。
func keyframeExtensionForMime(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ".png"
	}
}

// saveKeyframeImage は、basePath から index 番目のキーフレームパスを生成し、画像を保存します。
// RunAndSave / EditAndSave の両方から使われる共通の保存ロジックです。
func (r *CutKeyframeRunner) saveKeyframeImage(ctx context.Context, basePath string, index int, image *ports.KeyframeImage) (string, error) {
	keyframePath, err := generateIndexedPath(basePath, index)
	if err != nil {
		return "", fmt.Errorf("cut %d のキーフレーム出力パス生成に失敗しました: %w", index, err)
	}
	// 実際の画像形式に合わせて拡張子を付け替える（basePath は .png 基準）。
	if ext := keyframeExtensionForMime(image.MimeType); ext != path.Ext(keyframePath) {
		keyframePath = strings.TrimSuffix(keyframePath, path.Ext(keyframePath)) + ext
	}

	slog.InfoContext(ctx, "キーフレーム画像を保存しています",
		"index", index,
		"path", keyframePath,
	)

	if err := r.writer.Write(ctx, keyframePath, bytes.NewReader(image.Data),
		remoteio.WithContentType(image.MimeType),
		remoteio.WithCacheControl(r.cacheControl),
	); err != nil {
		return "", fmt.Errorf("cut %d のキーフレーム保存に失敗しました (path: %s): %w", index, keyframePath, err)
	}

	return keyframePath, nil
}

// cutImageEditor is implemented by image generators that can edit an existing single-cut
// keyframe image using a text instruction, instead of regenerating it from scratch.
type cutImageEditor interface {
	EditCut(ctx context.Context, cut ports.Cut, editPrompt string) (*ports.KeyframeImage, error)
}

// EditAndSave edits the existing keyframe image of recipe.Cuts[cutPosition] using editPrompt
// (preserving composition/pose rather than regenerating from scratch), saves the result the
// same way RunAndSave does, and returns the recipe with the updated KeyframeReference.
//
// 以前は単一カットのレシピしか受け付けず、呼び出し側が対象カットだけの使い捨てレシピを
// 組み立ててカットごとにループしていました。レシピ全体と対象位置を受け取ることで、
// 保存されるメタデータも常に完全なレシピになります。
func (r *CutKeyframeRunner) EditAndSave(ctx context.Context, recipe *ports.VideoRecipe, cutPosition int, editPrompt string, outputPath string) (*ports.VideoRecipe, error) {
	if recipe == nil {
		return nil, ports.ErrRecipeRequired
	}
	if r.writer == nil {
		return nil, ports.ErrWriterRequired
	}
	recipe.Normalize()
	if cutPosition < 0 || cutPosition >= len(recipe.Cuts) {
		return nil, fmt.Errorf("cut position %d is out of range (cuts=%d)", cutPosition, len(recipe.Cuts))
	}

	editor, ok := r.generator.(cutImageEditor)
	if !ok {
		return nil, ports.ErrEditingNotSupported
	}

	cut := recipe.Cuts[cutPosition]
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

	keyframePath, err := r.saveKeyframeImage(ctx, basePath, cutPosition+1, image)
	if err != nil {
		return nil, err
	}
	recipe.Cuts[cutPosition].KeyframeReference = keyframePath

	slog.InfoContext(ctx, "更新された動画メタデータを保存しています", "output_dir", targetDir)
	if _, err := writeRecipeMetadata(ctx, r.writer, targetDir, recipe); err != nil {
		return nil, err
	}

	return recipe, nil
}
