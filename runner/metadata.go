package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-veo-orchestrator/ports"
)

const metadataContentType = "application/json"

func buildRecipeMetadata(recipe *ports.VideoRecipe) ([]byte, error) {
	if recipe == nil {
		return nil, fmt.Errorf("VideoRecipe が nil です")
	}
	recipe.Normalize()

	data, err := json.MarshalIndent(recipe, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("動画メタデータのJSON変換に失敗しました: %w", err)
	}
	return data, nil
}

func writeRecipeMetadata(ctx context.Context, writer remoteio.Writer, outputDir string, recipe *ports.VideoRecipe) (string, error) {
	if writer == nil {
		return "", fmt.Errorf("writer is required")
	}

	metadata, err := buildRecipeMetadata(recipe)
	if err != nil {
		return "", err
	}

	metadataPath, err := resolveOutputPath(outputDir, defaultVideoMetaJSON)
	if err != nil {
		return "", fmt.Errorf("メタデータ出力パスの解決に失敗しました: %w", err)
	}

	if err := writer.Write(ctx, metadataPath, bytes.NewReader(metadata),
		remoteio.WithContentType(metadataContentType),
		remoteio.WithCacheControl(defaultCacheControl),
	); err != nil {
		return "", fmt.Errorf("動画メタデータの保存に失敗しました: %w", err)
	}

	return metadataPath, nil
}
