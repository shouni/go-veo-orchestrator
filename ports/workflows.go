package ports

import (
	"context"

	"github.com/shouni/go-veo-orchestrator/video"
)

// Workflows holds the constructed Runners.
//
// There is no Close. Reference images are now passed as gs:// URIs as-is, so
// the image cache and its periodic cleanup goroutine are gone (the call-interval
// limiter and singleflight hold no resources to release). Callers need no defer.
type Workflows struct {
	Script      ScriptRunner
	CutKeyframe CutKeyframeRunner
	Video       VideoTimelineRunner
	Publish     VideoPublishRunner
}

// ScriptRunner parses a source (URL or text) and generates a video script that includes the Music Recipe.
type ScriptRunner interface {
	Run(ctx context.Context, scriptURL string, mode string) (*video.Recipe, error)
}

// CutKeyframeRunner is the set of operations Workflows.CutKeyframe exposes to consumers.
//
// A generate-only Generate is deliberately absent. Receiving just the images
// without saving them would leave the recipe's KeyframeReference unset, and the
// consumer would end up reimplementing the filename convention (the two methods
// that include saving are the real entry points).
type CutKeyframeRunner interface {
	// GenerateAndSave generates and saves keyframes for the cuts that have none
	// yet, and returns the recipe with KeyframeReference updated.
	GenerateAndSave(ctx context.Context, recipe *video.Recipe, outputPath string) (*video.Recipe, error)
	// EditAndSave edits the existing keyframe of video.Cuts[cutPosition] with
	// editPrompt (keeping composition and pose rather than starting over), saves
	// it the same way as GenerateAndSave, and returns the recipe with
	// KeyframeReference updated. The target cut's KeyframeReference must point
	// at the source image. It returns an error when the injected image generator
	// does not support editing.
	EditAndSave(ctx context.Context, recipe *video.Recipe, cutPosition int, editPrompt string, outputPath string) (*video.Recipe, error)
}

// VideoPublishRunner writes the video recipe and the generated cuts' metadata out as JSON.
type VideoPublishRunner interface {
	Run(ctx context.Context, recipe *video.Recipe, outputDir string) (*video.PublishResult, error)
	BuildMetadata(recipe *video.Recipe) ([]byte, error)
}
