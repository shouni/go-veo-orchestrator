package runner

import (
	"context"
	"fmt"
	"io"
	"testing"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-veo-orchestrator/ports"
)

// fakeWriter records every Write call so tests can assert on the saved paths/content.
type fakeWriter struct {
	writes map[string][]byte
}

func newFakeWriter() *fakeWriter {
	return &fakeWriter{writes: make(map[string][]byte)}
}

func (w *fakeWriter) Write(_ context.Context, path string, contentReader io.Reader, _ ...remoteio.WriteOption) error {
	data, err := io.ReadAll(contentReader)
	if err != nil {
		return err
	}
	w.writes[path] = data
	return nil
}

// fakeCutImageGenerator implements both ports.CutImageGenerator and cutImageEditor so tests
// can control EditAndSave's behavior independently of the real keyframe.Generator.
type fakeCutImageGenerator struct {
	editFunc func(ctx context.Context, cut ports.Cut, editPrompt string) (*imagePorts.ImageResponse, error)
}

func (f *fakeCutImageGenerator) Execute(_ context.Context, _ []ports.Cut) ([]*imagePorts.ImageResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (f *fakeCutImageGenerator) EditCut(ctx context.Context, cut ports.Cut, editPrompt string) (*imagePorts.ImageResponse, error) {
	if f.editFunc != nil {
		return f.editFunc(ctx, cut, editPrompt)
	}
	return &imagePorts.ImageResponse{Data: []byte("edited"), MimeType: "image/png"}, nil
}

// nonEditingCutImageGenerator implements ports.CutImageGenerator only, to exercise the
// "generator does not support editing" error path.
type nonEditingCutImageGenerator struct{}

func (nonEditingCutImageGenerator) Execute(_ context.Context, _ []ports.Cut) ([]*imagePorts.ImageResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func TestCutKeyframeRunner_EditAndSave(t *testing.T) {
	ctx := context.Background()

	t.Run("edits the single cut and saves keyframe + metadata", func(t *testing.T) {
		var captured struct {
			cut        ports.Cut
			editPrompt string
		}
		gen := &fakeCutImageGenerator{
			editFunc: func(_ context.Context, cut ports.Cut, editPrompt string) (*imagePorts.ImageResponse, error) {
				captured.cut = cut
				captured.editPrompt = editPrompt
				return &imagePorts.ImageResponse{Data: []byte("edited-bytes"), MimeType: "image/png"}, nil
			},
		}
		writer := newFakeWriter()
		r := NewCutKeyframeRunner(gen, writer)

		recipe := &ports.VideoRecipe{
			Cuts: []ports.Cut{
				{CutIndex: 2, CharacterID: "zundamon", KeyframeReference: "gs://bucket/jobs/j1/images/keyframe_2.png"},
			},
		}

		got, err := r.EditAndSave(ctx, recipe, "腕には絆創膏を1〜2枚のみ", "gs://bucket/jobs/regen-1/regens/cut-2/")
		if err != nil {
			t.Fatalf("EditAndSave failed: %v", err)
		}
		if captured.editPrompt != "腕には絆創膏を1〜2枚のみ" {
			t.Errorf("edit prompt = %q", captured.editPrompt)
		}
		if captured.cut.KeyframeReference != "gs://bucket/jobs/j1/images/keyframe_2.png" {
			t.Errorf("editor received wrong source keyframe: %q", captured.cut.KeyframeReference)
		}
		if got.Cuts[0].KeyframeReference == "gs://bucket/jobs/j1/images/keyframe_2.png" {
			t.Error("expected KeyframeReference to be updated to the newly saved path")
		}
		if len(writer.writes) != 2 {
			t.Fatalf("expected 2 writes (keyframe + metadata), got %d: %v", len(writer.writes), writer.writes)
		}
	})

	t.Run("errors when recipe has more than one cut", func(t *testing.T) {
		r := NewCutKeyframeRunner(&fakeCutImageGenerator{}, newFakeWriter())
		recipe := &ports.VideoRecipe{Cuts: []ports.Cut{{CutIndex: 1}, {CutIndex: 2}}}

		if _, err := r.EditAndSave(ctx, recipe, "edit", "gs://bucket/out/"); err == nil {
			t.Fatal("expected error for multi-cut recipe")
		}
	})

	t.Run("errors when cut has no existing keyframe", func(t *testing.T) {
		r := NewCutKeyframeRunner(&fakeCutImageGenerator{}, newFakeWriter())
		recipe := &ports.VideoRecipe{Cuts: []ports.Cut{{CutIndex: 1}}}

		if _, err := r.EditAndSave(ctx, recipe, "edit", "gs://bucket/out/"); err == nil {
			t.Fatal("expected error for cut with no KeyframeReference")
		}
	})

	t.Run("errors when generator does not support editing", func(t *testing.T) {
		r := NewCutKeyframeRunner(nonEditingCutImageGenerator{}, newFakeWriter())
		recipe := &ports.VideoRecipe{Cuts: []ports.Cut{{CutIndex: 1, KeyframeReference: "gs://bucket/k.png"}}}

		if _, err := r.EditAndSave(ctx, recipe, "edit", "gs://bucket/out/"); err == nil {
			t.Fatal("expected error when generator does not implement cutImageEditor")
		}
	})
}
