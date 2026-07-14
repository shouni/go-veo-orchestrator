package runner

import (
	"context"
	"errors"
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

// recordingCutImageGenerator implements ports.CutImageGenerator and records how many times
// Execute was called, so tests can drive Run/RunAndSave independently of EditAndSave.
type recordingCutImageGenerator struct {
	images []*imagePorts.ImageResponse
	err    error
	calls  int
}

func (g *recordingCutImageGenerator) Execute(_ context.Context, _ []ports.Cut) ([]*imagePorts.ImageResponse, error) {
	g.calls++
	if g.err != nil {
		return nil, g.err
	}
	return g.images, nil
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

		_, err := r.EditAndSave(ctx, recipe, "edit", "gs://bucket/out/")
		if !errors.Is(err, ports.ErrEditingNotSupported) {
			t.Fatalf("expected ErrEditingNotSupported, got %v", err)
		}
	})

	t.Run("returns ErrRecipeRequired for nil recipe", func(t *testing.T) {
		r := NewCutKeyframeRunner(&fakeCutImageGenerator{}, newFakeWriter())

		_, err := r.EditAndSave(ctx, nil, "edit", "gs://bucket/out/")
		if !errors.Is(err, ports.ErrRecipeRequired) {
			t.Fatalf("expected ErrRecipeRequired, got %v", err)
		}
	})
}

func TestCutKeyframeRunner_Run(t *testing.T) {
	ctx := context.Background()

	t.Run("returns generated images for the recipe's cuts", func(t *testing.T) {
		want := []*imagePorts.ImageResponse{
			{Data: []byte("a"), MimeType: "image/png"},
			{Data: []byte("b"), MimeType: "image/png"},
		}
		gen := &recordingCutImageGenerator{images: want}
		r := NewCutKeyframeRunner(gen, newFakeWriter())
		recipe := &ports.VideoRecipe{Cuts: []ports.Cut{{CutIndex: 1}, {CutIndex: 2}}}

		got, err := r.Run(ctx, recipe)
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("len(got) = %d, want 2", len(got))
		}
		if gen.calls != 1 {
			t.Fatalf("generator calls = %d, want 1", gen.calls)
		}
	})

	t.Run("returns ErrRecipeRequired for nil recipe", func(t *testing.T) {
		r := NewCutKeyframeRunner(&recordingCutImageGenerator{}, newFakeWriter())

		_, err := r.Run(ctx, nil)
		if !errors.Is(err, ports.ErrRecipeRequired) {
			t.Fatalf("expected ErrRecipeRequired, got %v", err)
		}
	})

	t.Run("wraps generator failure", func(t *testing.T) {
		gen := &recordingCutImageGenerator{err: fmt.Errorf("upstream failure")}
		r := NewCutKeyframeRunner(gen, newFakeWriter())
		recipe := &ports.VideoRecipe{Cuts: []ports.Cut{{CutIndex: 1}}}

		if _, err := r.Run(ctx, recipe); err == nil {
			t.Fatal("expected error when generator fails")
		}
	})
}

func TestCutKeyframeRunner_RunAndSave(t *testing.T) {
	ctx := context.Background()

	t.Run("saves indexed keyframes and updated metadata", func(t *testing.T) {
		images := []*imagePorts.ImageResponse{
			{Data: []byte("img-1"), MimeType: "image/png"},
			{Data: []byte("img-2"), MimeType: "image/png"},
		}
		gen := &recordingCutImageGenerator{images: images}
		writer := newFakeWriter()
		r := NewCutKeyframeRunner(gen, writer)
		recipe := &ports.VideoRecipe{Cuts: []ports.Cut{{CutIndex: 1}, {CutIndex: 2}}}

		got, err := r.RunAndSave(ctx, recipe, "gs://bucket/jobs/j1/")
		if err != nil {
			t.Fatalf("RunAndSave() error = %v", err)
		}
		if got.Cuts[0].KeyframeReference == "" || got.Cuts[1].KeyframeReference == "" {
			t.Fatal("expected KeyframeReference to be set for all cuts")
		}
		if got.Cuts[0].KeyframeReference == got.Cuts[1].KeyframeReference {
			t.Fatalf("expected distinct indexed paths per cut, both = %q", got.Cuts[0].KeyframeReference)
		}
		if len(writer.writes) != 3 { // 2 keyframes + metadata
			t.Fatalf("expected 3 writes, got %d: %v", len(writer.writes), writer.writes)
		}
	})

	t.Run("errors when image count does not match cut count", func(t *testing.T) {
		gen := &recordingCutImageGenerator{
			images: []*imagePorts.ImageResponse{{Data: []byte("only-one"), MimeType: "image/png"}},
		}
		r := NewCutKeyframeRunner(gen, newFakeWriter())
		recipe := &ports.VideoRecipe{Cuts: []ports.Cut{{CutIndex: 1}, {CutIndex: 2}}}

		if _, err := r.RunAndSave(ctx, recipe, "gs://bucket/out/"); err == nil {
			t.Fatal("expected error for image/cut count mismatch")
		}
	})

	t.Run("returns ErrRecipeRequired for nil recipe", func(t *testing.T) {
		r := NewCutKeyframeRunner(&recordingCutImageGenerator{}, newFakeWriter())

		_, err := r.RunAndSave(ctx, nil, "gs://bucket/out/")
		if !errors.Is(err, ports.ErrRecipeRequired) {
			t.Fatalf("expected ErrRecipeRequired, got %v", err)
		}
	})
}
