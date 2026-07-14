package ports

import (
	"context"
	"errors"
	"testing"
)

func TestNoopVideoTimelineRunner(t *testing.T) {
	ctx := context.Background()
	runner := NewNoopVideoTimelineRunner()

	t.Run("Run always fails with ErrVideoRunnerNotConfigured", func(t *testing.T) {
		if _, err := runner.Run(ctx, &VideoRecipe{}); !errors.Is(err, ErrVideoRunnerNotConfigured) {
			t.Fatalf("expected ErrVideoRunnerNotConfigured, got %v", err)
		}
	})

	t.Run("RunAndSave always fails with ErrVideoRunnerNotConfigured", func(t *testing.T) {
		if _, err := runner.RunAndSave(ctx, &VideoRecipe{}, "gs://bucket/out/"); !errors.Is(err, ErrVideoRunnerNotConfigured) {
			t.Fatalf("expected ErrVideoRunnerNotConfigured, got %v", err)
		}
	})
}
