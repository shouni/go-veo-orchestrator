// Package runner provides the runners that carry out a video recipe (keyframe
// generation, script execution, video generation, publishing).
package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"strings"
	"time"

	"github.com/shouni/genai-kit/imagegen"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-veo-orchestrator/ports"
	"github.com/shouni/go-veo-orchestrator/video"
	"golang.org/x/sync/errgroup"
)

// DefaultKeyframeCacheControl is the default Cache-Control header put on saved
// keyframe images (replaceable with WithCacheControl).
const DefaultKeyframeCacheControl = "public, max-age=1800"

// metadataSaveTimeout bounds the metadata save performed on a detached ctx.
const metadataSaveTimeout = 30 * time.Second

// CutKeyframeRunner manages cut keyframe generation based on a video recipe.
//
// The injected remoteio.Writer must be safe for concurrent use. GenerateAndSave
// finishes the saving inside each cut's goroutine, so Write is called
// concurrently whenever WithMaxConcurrency is 2 or more.
type CutKeyframeRunner struct {
	generator      ports.CutImageGenerator
	writer         remoteio.Writer
	cacheControl   string
	maxConcurrency int
}

// KeyframeRunnerOption is an optional setting of CutKeyframeRunner.
type KeyframeRunnerOption func(*CutKeyframeRunner)

// WithCacheControl replaces the Cache-Control header put on saved keyframe images.
// The default is DefaultKeyframeCacheControl (public caching allowed); a deployment
// that does not want its outputs public should specify "private" or similar.
func WithCacheControl(value string) KeyframeRunnerOption {
	return func(r *CutKeyframeRunner) {
		if value != "" {
			r.cacheControl = value
		}
	}
}

// WithMaxConcurrency sets how many keyframe generations run at once across cuts.
// 1 or less (the default is 1) means sequential.
//
// Concurrency lives here, with the saving, rather than on the image generation
// side (keyframe.Generator) so that one cut's "generate → save" completes within a
// single goroutine. For how it relates to the call interval, see
// ports.Config.MaxConcurrency.
func WithMaxConcurrency(n int) KeyframeRunnerOption {
	return func(r *CutKeyframeRunner) {
		if n > 0 {
			r.maxConcurrency = n
		}
	}
}

// NewCutKeyframeRunner initializes the runner with its dependencies injected.
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

// GenerateAndSave generates keyframes for the cuts that have no keyframe image
// yet, saving each one the moment it is produced, and updates KeyframeReference
// and KeyframeSeed.
//
// Generation and saving are paired one image at a time for operational reasons.
// If images were generated as a batch and saved afterwards, a process dying in
// between (Cloud Run timeout, deploy, OOM) would lose the generated — and
// therefore billed — images with the memory, and a rerun would remake all of
// them. Saving one at a time loses at most one image, and the run resumes from
// the recipe's KeyframeReference. To keep those references, the final metadata
// save happens even when the caller's ctx has ended (see saveMetadata).
//
// Cuts that already have a KeyframeReference are not re-baked. The recipe is
// treated as "the desired state" and only the missing keyframes are filled in —
// the same idea as VideoTimelineRunner.Run skipping cuts where
// Cut.IsGenerated(). When every cut is complete, image generation is never
// called and only the metadata is saved.
//
// Generation runs maxConcurrency-wide, but saving happens right after each cut
// is generated. When some cuts fail, the rest are not cancelled: the images and
// metadata that succeeded are saved, and then the errors are returned
// aggregated with errors.Join.
//
// To re-bake a cut, the caller clears its KeyframeReference before passing it
// in ("empty = not generated" is this method's only criterion). EditAndSave is a
// separate path that uses the existing image as its editing source, so this
// criterion does not apply to it.
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

	// Return the recipe even if saving the metadata fails. The KeyframeReference
	// values pointing at the saved images exist only in the recipe, which leaves
	// the caller room to save it again itself.
	if err := r.saveMetadata(ctx, targetDir, recipe); err != nil {
		return recipe, errors.Join(err, genErr)
	}

	return recipe, genErr
}

// saveMetadata saves the updated recipe's metadata on a ctx detached from the
// caller's.
//
// When the reason for getting here is the cancellation itself (the caller's
// timeout), ctx is already Done. Using it as-is would make the write fail every
// time, leaving no reference to the saved — and billed — images anywhere, and
// GenerateAndSave's "resume where it stopped" would not hold. It is detached and
// then given a fresh bound so that an unresponsive destination cannot keep it from
// returning.
func (r *CutKeyframeRunner) saveMetadata(ctx context.Context, targetDir string, recipe *video.Recipe) error {
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), metadataSaveTimeout)
	defer cancel()

	slog.InfoContext(saveCtx, "Saving updated video metadata", "output_dir", targetDir)
	_, err := writeRecipeMetadata(saveCtx, r.writer, targetDir, recipe)
	return err
}

// generateAndSaveCuts processes the ungenerated cuts in parallel.
//
// Each element of recipe.Cuts is written only by the goroutine assigned to it (no
// two share an index), so no locking is needed.
func (r *CutKeyframeRunner) generateAndSaveCuts(ctx context.Context, recipe *video.Recipe, basePath string) error {
	pending := pendingKeyframeCutPositions(recipe.Cuts)
	if len(pending) == 0 {
		slog.InfoContext(ctx, "Skipped keyframe generation because every cut already has a keyframe",
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
			// After the caller cancels, start no new generation (keep the saved results).
			if err := ctx.Err(); err != nil {
				errs[n] = err
				return nil
			}
			errs[n] = r.generateAndSaveCut(ctx, recipe, basePath, position, n+1, len(pending))
			return nil
		})
	}
	// The closures always return nil, so Wait returns no error either (individual failures are collected in errs).
	_ = eg.Wait()

	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("cut keyframe generation failed: %w", err)
	}
	slog.InfoContext(ctx, "Successfully generated cut keyframes", "count", len(pending))
	return nil
}

// generateAndSaveCut generates one cut, saves it and reflects it into the recipe.
// The saved filename uses the position in the recipe (1-based), so that narrowing
// the generation targets does not shift the numbers away from the parent recipe's
// order, which keeps a partial batch from overwriting another cut's file.
func (r *CutKeyframeRunner) generateAndSaveCut(
	ctx context.Context, recipe *video.Recipe, basePath string, position, index, total int,
) error {
	image, err := r.generator.GenerateCut(ctx, recipe.Cuts[position], index, total)
	if err != nil {
		return err
	}
	if image == nil {
		return fmt.Errorf("cut %d: no keyframe image was returned", recipe.Cuts[position].CutIndex)
	}

	keyframePath, err := r.saveKeyframeImage(ctx, basePath, position+1, image)
	if err != nil {
		return err
	}

	recipe.Cuts[position].KeyframeReference = keyframePath
	recipe.Cuts[position].KeyframeSeed = image.UsedSeed
	return nil
}

// pendingKeyframeCutPositions returns the positions (0-based) of cuts that have no
// keyframe image yet. The only criterion is whether KeyframeReference is empty;
// this is separate from Cut.IsGenerated() (video generation completed). A keyframe
// already baked while the video is not yet generated is a perfectly normal state.
func pendingKeyframeCutPositions(cuts []video.Cut) []int {
	var pending []int
	for i := range cuts {
		if strings.TrimSpace(cuts[i].KeyframeReference) == "" {
			pending = append(pending, i)
		}
	}
	return pending
}

// resolveKeyframeBasePath resolves the save directory and the base for indexed
// keyframe paths that GenerateAndSave / EditAndSave both need.
func resolveKeyframeBasePath(outputPath string) (targetDir string, basePath string, err error) {
	targetDir = resolveBaseURL(outputPath)
	basePath, err = resolveOutputPath(targetDir, defaultKeyframePath())
	if err != nil {
		return "", "", fmt.Errorf("failed to resolve the output path: %w", err)
	}
	return targetDir, basePath, nil
}

// saveKeyframeImage builds the index-th keyframe path from basePath and saves the image.
func (r *CutKeyframeRunner) saveKeyframeImage(ctx context.Context, basePath string, index int, image *video.KeyframeImage) (string, error) {
	keyframePath, err := generateIndexedPath(basePath, index)
	if err != nil {
		return "", fmt.Errorf("cut %d: failed to build the keyframe output path: %w", index, err)
	}
	// Swap the extension to match the actual image format (basePath assumes .png).
	if ext := imagegen.ExtensionByMIMEType(image.MimeType); ext != path.Ext(keyframePath) {
		keyframePath = strings.TrimSuffix(keyframePath, path.Ext(keyframePath)) + ext
	}

	slog.InfoContext(ctx, "Saving keyframe image",
		"index", index,
		"path", keyframePath,
	)

	if err := r.writer.Write(ctx, keyframePath, bytes.NewReader(image.Data),
		remoteio.WithContentType(image.MimeType),
		remoteio.WithCacheControl(r.cacheControl),
	); err != nil {
		return "", fmt.Errorf("cut %d: failed to save the keyframe (path: %s): %w", index, keyframePath, err)
	}

	return keyframePath, nil
}

// cutImageEditor is the interface satisfied by image generators that can edit an
// existing keyframe image according to a text instruction (editing, not remaking).
type cutImageEditor interface {
	EditCut(ctx context.Context, cut video.Cut, editPrompt string) (*video.KeyframeImage, error)
}

// EditAndSave implements ports.CutKeyframeRunner.EditAndSave.
//
// It takes the whole recipe and the target position so the saved metadata is
// always the complete recipe (it used to accept only a single-cut recipe, and
// callers looped, building a throwaway recipe holding just the target cut).
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

	slog.InfoContext(ctx, "Editing keyframe", "cut_index", cut.CutIndex)
	image, err := editor.EditCut(ctx, cut, editPrompt)
	if err != nil {
		return nil, fmt.Errorf("cut %d keyframe edit failed: %w", cut.CutIndex, err)
	}

	keyframePath, err := r.saveKeyframeImage(ctx, basePath, cutPosition+1, image)
	if err != nil {
		return nil, err
	}
	recipe.Cuts[cutPosition].KeyframeReference = keyframePath

	if err := r.saveMetadata(ctx, targetDir, recipe); err != nil {
		return nil, err
	}

	return recipe, nil
}
