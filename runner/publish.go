package runner

import (
	"context"
	"fmt"
	"path"

	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-veo-orchestrator/ports"
	"github.com/shouni/go-veo-orchestrator/video"
)

// VideoPublisherRunner writes the Music Recipe and cut information out as video metadata.
type VideoPublisherRunner struct {
	writer remoteio.Writer
}

// NewVideoPublisherRunner initializes a VideoPublisherRunner.
func NewVideoPublisherRunner(writer remoteio.Writer) *VideoPublisherRunner {
	return &VideoPublisherRunner{writer: writer}
}

// Run saves only the JSON metadata used for joining and validation, not HTML/Markdown.
func (pr *VideoPublisherRunner) Run(ctx context.Context, recipe *video.Recipe, outputDir string) (*video.PublishResult, error) {
	if recipe == nil {
		return nil, ports.ErrRecipeRequired
	}
	if pr.writer == nil {
		return nil, fmt.Errorf("writer is required")
	}

	recipe.Normalize()
	metadataPath, err := writeRecipeMetadata(ctx, pr.writer, outputDir, recipe)
	if err != nil {
		return nil, err
	}

	imagePaths := make([]string, 0, len(recipe.Cuts))
	for _, cut := range recipe.Cuts {
		if cut.KeyframeReference == "" {
			continue
		}
		imagePaths = append(imagePaths, path.Join(defaultImageDir, path.Base(cut.KeyframeReference)))
	}

	return &video.PublishResult{
		MetadataPath: metadataPath,
		ImagePaths:   imagePaths,
	}, nil
}

// BuildMetadata converts a VideoRecipe to indented JSON.
func (pr *VideoPublisherRunner) BuildMetadata(recipe *video.Recipe) ([]byte, error) {
	return buildRecipeMetadata(recipe)
}
