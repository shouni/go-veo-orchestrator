package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-veo-orchestrator/ports"
	"github.com/shouni/go-veo-orchestrator/video"
)

const metadataContentType = "application/json"

func buildRecipeMetadata(recipe *video.Recipe) ([]byte, error) {
	if recipe == nil {
		return nil, ports.ErrRecipeRequired
	}

	data, err := json.MarshalIndent(recipe, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal video metadata to JSON: %w", err)
	}
	return data, nil
}

func writeRecipeMetadata(ctx context.Context, writer remoteio.Writer, outputDir string, recipe *video.Recipe) (string, error) {
	if writer == nil {
		return "", fmt.Errorf("writer is required")
	}

	metadata, err := buildRecipeMetadata(recipe)
	if err != nil {
		return "", err
	}

	metadataPath, err := resolveOutputPath(outputDir, defaultVideoMetaJSON)
	if err != nil {
		return "", fmt.Errorf("failed to resolve the metadata output path: %w", err)
	}

	// Metadata is an output placed in the same job directory as the keyframes,
	// so it uses the same caching policy (the name comes from the keyframe side).
	if err := writer.Write(ctx, metadataPath, bytes.NewReader(metadata),
		remoteio.WithContentType(metadataContentType),
		remoteio.WithCacheControl(DefaultKeyframeCacheControl),
	); err != nil {
		return "", fmt.Errorf("failed to save video metadata: %w", err)
	}

	return metadataPath, nil
}
