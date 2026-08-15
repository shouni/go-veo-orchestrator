package runner

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/shouni/go-veo-orchestrator/ports"
	"github.com/shouni/go-veo-orchestrator/video"
)

func TestVideoPublisherRunner_Run(t *testing.T) {
	ctx := context.Background()

	t.Run("writes metadata and collects image paths", func(t *testing.T) {
		writer := newFakeWriter()
		pr := NewVideoPublisherRunner(writer)
		recipe := &video.Recipe{
			ProjectTitle: "test",
			Cuts: []video.Cut{
				{CutIndex: 1, KeyframeResult: video.KeyframeResult{KeyframeReference: "gs://bucket/jobs/j1/images/keyframe_1.png"}},
				{CutIndex: 2},
			},
		}

		result, err := pr.Run(ctx, recipe, "gs://bucket/jobs/j1/")
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if result.MetadataPath == "" {
			t.Fatal("expected a non-empty MetadataPath")
		}
		if len(result.ImagePaths) != 1 {
			t.Fatalf("expected 1 image path (empty KeyframeReference skipped), got %d: %v", len(result.ImagePaths), result.ImagePaths)
		}
		if _, ok := writer.writes[result.MetadataPath]; !ok {
			t.Fatalf("expected metadata written to %q, writes: %v", result.MetadataPath, writer.writes)
		}
	})

	t.Run("returns ErrRecipeRequired for nil recipe", func(t *testing.T) {
		pr := NewVideoPublisherRunner(newFakeWriter())

		_, err := pr.Run(ctx, nil, "gs://bucket/out/")
		if !errors.Is(err, ports.ErrRecipeRequired) {
			t.Fatalf("expected ErrRecipeRequired, got %v", err)
		}
	})

	t.Run("errors when writer is nil", func(t *testing.T) {
		pr := NewVideoPublisherRunner(nil)
		recipe := &video.Recipe{Cuts: []video.Cut{{CutIndex: 1}}}

		if _, err := pr.Run(ctx, recipe, "gs://bucket/out/"); err == nil {
			t.Fatal("expected error when writer is nil")
		}
	})
}

func TestVideoPublisherRunner_BuildMetadata(t *testing.T) {
	pr := NewVideoPublisherRunner(newFakeWriter())
	recipe := &video.Recipe{ProjectTitle: "meta test", Cuts: []video.Cut{{CutIndex: 1}}}

	data, err := pr.BuildMetadata(recipe)
	if err != nil {
		t.Fatalf("BuildMetadata() error = %v", err)
	}

	var got video.Recipe
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if got.ProjectTitle != "meta test" {
		t.Fatalf("ProjectTitle = %q, want meta test", got.ProjectTitle)
	}
}
