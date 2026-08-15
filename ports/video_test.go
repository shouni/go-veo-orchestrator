package ports

import (
	"context"
	"errors"
	"testing"

	"github.com/shouni/go-veo-orchestrator/video"
)

func TestNoopVideoTimelineRunner(t *testing.T) {
	ctx := context.Background()
	runner := NewNoopVideoTimelineRunner()

	t.Run("Run always fails with ErrVideoRunnerNotConfigured", func(t *testing.T) {
		if _, err := runner.Run(ctx, &video.Recipe{}); !errors.Is(err, ErrVideoRunnerNotConfigured) {
			t.Fatalf("expected ErrVideoRunnerNotConfigured, got %v", err)
		}
	})
}
