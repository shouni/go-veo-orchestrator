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

	"github.com/shouni/genai-kit/imagegen"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-veo-orchestrator/ports"
	"github.com/shouni/go-veo-orchestrator/video"
	"golang.org/x/sync/errgroup"
)

// DefaultKeyframeCacheControl は、保存するキーフレーム画像に付ける既定の
// Cache-Control ヘッダです（WithCacheControl で差し替え可能）。
const DefaultKeyframeCacheControl = "public, max-age=1800"

// CutKeyframeRunner は、動画レシピを元にカットキーフレーム生成を管理します。
//
// **注入する remoteio.Writer は同時アクセス安全である必要があります。**
// GenerateAndSave はカットごとの goroutine の中で保存まで済ませるため、
// WithMaxConcurrency が 2 以上なら Write は並行に呼ばれます。
type CutKeyframeRunner struct {
	generator      ports.CutImageGenerator
	writer         remoteio.Writer
	cacheControl   string
	maxConcurrency int
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

// WithMaxConcurrency は、カット間のキーフレーム生成の同時実行数を設定します。
// 1 以下（既定は 1）なら逐次実行です。
//
// 並列度を画像生成側（keyframe.Generator）ではなく保存を持つこちらに置くのは、
// 1 カットの「生成 → 保存」を 1 つの goroutine で完結させるためです。発射間隔との
// 関係は ports.Config.MaxConcurrency を参照してください。
func WithMaxConcurrency(n int) KeyframeRunnerOption {
	return func(r *CutKeyframeRunner) {
		if n > 0 {
			r.maxConcurrency = n
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
		generator:      generator,
		writer:         writer,
		cacheControl:   DefaultKeyframeCacheControl,
		maxConcurrency: 1,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(r)
		}
	}
	return r
}

// GenerateAndSave は、キーフレーム画像をまだ持たないカットについて
// **1 枚生成するたびに即座に保存し**、KeyframeReference と KeyframeSeed を更新します。
//
// 生成と保存を 1 枚ずつ組にしているのは運用上の理由です。まとめて生成してから保存すると、
// その間にプロセスが落ちた場合（Cloud Run のタイムアウト、デプロイ、OOM）に生成済み＝
// 課金済みの画像がメモリごと消え、再実行で全部作り直しになります。1 枚ずつ保存していれば
// 失われるのは最大 1 枚で、レシピの KeyframeReference を見て続きから再開できます。
//
// **すでに KeyframeReference を持つカットは焼き直しません。** レシピを「あるべき状態」
// として扱い、足りないキーフレームだけを補います。これは VideoTimelineRunner.Run が
// Cut.IsGenerated() のカットを飛ばすのと同じ考え方です。全カットが揃っていれば画像生成は
// 一度も呼ばれず、メタデータの保存だけを行います。
//
// 生成は maxConcurrency 並列で走りますが、保存は各カットの生成直後に行われます。
// 一部が失敗しても残りは打ち切らず、成功した分の画像とメタデータを保存してから
// エラーを errors.Join で集約して返します。
//
// 焼き直したいカットは、呼び出し側が KeyframeReference を空にしてから渡してください
// （「空 = 未生成」がこのメソッドの唯一の判定基準です）。EditAndSave は既存画像を
// 編集ソースにする別経路なので、この判定の対象外です。
func (r *CutKeyframeRunner) GenerateAndSave(ctx context.Context, recipe *video.Recipe, outputPath string) (*video.Recipe, error) {
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

	genErr := r.generateAndSaveCuts(ctx, recipe, basePath)

	slog.InfoContext(ctx, "更新された動画メタデータを保存しています", "output_dir", targetDir)
	if _, err := writeRecipeMetadata(ctx, r.writer, targetDir, recipe); err != nil {
		return nil, errors.Join(err, genErr)
	}

	return recipe, genErr
}

// generateAndSaveCuts は未生成のカットを並列に処理します。
//
// recipe.Cuts の各要素は担当 goroutine だけが書き込むため（添字が重複しない）、
// 排他は不要です。
func (r *CutKeyframeRunner) generateAndSaveCuts(ctx context.Context, recipe *video.Recipe, basePath string) error {
	pending := pendingKeyframeCutPositions(recipe.Cuts)
	if len(pending) == 0 {
		slog.InfoContext(ctx, "全カットにキーフレームがあるため生成をスキップしました",
			"cuts", len(recipe.Cuts))
		return nil
	}

	slog.InfoContext(ctx, "Starting cut keyframe generation",
		"cuts", len(pending), "skipped", len(recipe.Cuts)-len(pending),
		"concurrency", r.maxConcurrency)

	errs := make([]error, len(pending))

	var eg errgroup.Group
	eg.SetLimit(r.maxConcurrency)
	for n, position := range pending {
		eg.Go(func() error {
			// 呼び出し元のキャンセル後は新しい生成を始めない（保存済みの結果は残す）。
			if err := ctx.Err(); err != nil {
				errs[n] = err
				return nil
			}
			errs[n] = r.generateAndSaveCut(ctx, recipe, basePath, position, n+1, len(pending))
			return nil
		})
	}
	// クロージャは常に nil を返すため Wait もエラーを返しません（個々の失敗は errs に集約）。
	_ = eg.Wait()

	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("cut keyframe generation failed: %w", err)
	}
	slog.InfoContext(ctx, "Successfully generated cut keyframes", "count", len(pending))
	return nil
}

// generateAndSaveCut は 1 カット分を生成し、保存し、レシピへ反映します。
// 保存ファイル名にはレシピ内の位置（1 始まり）を使います。生成対象を絞っても
// 番号が親レシピの並びからずれないようにするためで、部分バッチが別カットの
// ファイルを上書きするのを防ぎます。
func (r *CutKeyframeRunner) generateAndSaveCut(
	ctx context.Context, recipe *video.Recipe, basePath string, position, index, total int,
) error {
	image, err := r.generator.GenerateCut(ctx, recipe.Cuts[position], index, total)
	if err != nil {
		return err
	}
	if image == nil {
		return fmt.Errorf("cut %d: キーフレーム画像が返りませんでした", recipe.Cuts[position].CutIndex)
	}

	keyframePath, err := r.saveKeyframeImage(ctx, basePath, position+1, image)
	if err != nil {
		return err
	}

	recipe.Cuts[position].KeyframeReference = keyframePath
	recipe.Cuts[position].KeyframeSeed = image.UsedSeed
	return nil
}

// pendingKeyframeCutPositions は、まだキーフレーム画像を持たないカットの位置（0始まり）を
// 返します。判定は KeyframeReference が空かどうかだけで、Cut.IsGenerated()（動画生成の
// 完了）とは別物です。動画が未生成でもキーフレームは焼き済み、という状態が普通にあります。
func pendingKeyframeCutPositions(cuts []video.Cut) []int {
	var pending []int
	for i := range cuts {
		if strings.TrimSpace(cuts[i].KeyframeReference) == "" {
			pending = append(pending, i)
		}
	}
	return pending
}

// resolveKeyframeBasePath は、GenerateAndSave / EditAndSave が共通して必要とする保存先ディレクトリと
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
// GenerateAndSave / EditAndSave の両方から使われる共通の保存ロジックです。
func (r *CutKeyframeRunner) saveKeyframeImage(ctx context.Context, basePath string, index int, image *video.KeyframeImage) (string, error) {
	keyframePath, err := generateIndexedPath(basePath, index)
	if err != nil {
		return "", fmt.Errorf("cut %d のキーフレーム出力パス生成に失敗しました: %w", index, err)
	}
	// 実際の画像形式に合わせて拡張子を付け替える（basePath は .png 基準）。
	if ext := imagegen.ExtensionByMIMEType(image.MimeType); ext != path.Ext(keyframePath) {
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

// cutImageEditor は、既存のキーフレーム画像をテキスト指示で編集できる画像生成器が
// 満たすインターフェースです（作り直しではなく編集）。
type cutImageEditor interface {
	EditCut(ctx context.Context, cut video.Cut, editPrompt string) (*video.KeyframeImage, error)
}

// EditAndSave は ports.CutKeyframeRunner.EditAndSave の実装です。
//
// レシピ全体と対象位置を受け取るのは、保存されるメタデータを常に完全なレシピに
// 保つためです（以前は単一カットのレシピしか受け付けず、呼び出し側が対象カットだけの
// 使い捨てレシピを組み立ててループしていました）。
func (r *CutKeyframeRunner) EditAndSave(ctx context.Context, recipe *video.Recipe, cutPosition int, editPrompt string, outputPath string) (*video.Recipe, error) {
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
