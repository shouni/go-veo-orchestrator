package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
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
				{CutIndex: 2, CharacterID: "zundamon", KeyframeResult: ports.KeyframeResult{KeyframeReference: "gs://bucket/jobs/j1/images/keyframe_2.png"}},
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
		recipe := &ports.VideoRecipe{Cuts: []ports.Cut{{CutIndex: 1, KeyframeResult: ports.KeyframeResult{KeyframeReference: "gs://bucket/k.png"}}}}

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

	t.Run("wraps generator failure", func(t *testing.T) {
		gen := &recordingCutImageGenerator{err: fmt.Errorf("upstream failure")}
		r := NewCutKeyframeRunner(gen, newFakeWriter())
		recipe := &ports.VideoRecipe{Cuts: []ports.Cut{{CutIndex: 1}}}

		if _, err := r.RunAndSave(ctx, recipe, "gs://bucket/out/"); err == nil {
			t.Fatal("expected error when generator fails")
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

// capturingCutImageGenerator records the cuts it was asked to generate, so tests can assert
// which cuts RunAndSave decided still needed a keyframe.
type capturingCutImageGenerator struct {
	seen  [][]ports.Cut
	image func(cut ports.Cut) *imagePorts.ImageResponse
}

func (g *capturingCutImageGenerator) Execute(_ context.Context, cuts []ports.Cut) ([]*imagePorts.ImageResponse, error) {
	g.seen = append(g.seen, append([]ports.Cut(nil), cuts...))
	images := make([]*imagePorts.ImageResponse, 0, len(cuts))
	for _, cut := range cuts {
		if g.image != nil {
			images = append(images, g.image(cut))
			continue
		}
		images = append(images, &imagePorts.ImageResponse{Data: []byte("generated"), MimeType: "image/png"})
	}
	return images, nil
}

func keyframeCut(index int, keyframeReference string) ports.Cut {
	return ports.Cut{
		CutIndex:       index,
		VisualAnchor:   fmt.Sprintf("anchor %d", index),
		AudioSync:      ports.AudioSync{DurationSec: 8},
		KeyframeResult: ports.KeyframeResult{KeyframeReference: keyframeReference},
	}
}

// TestCutKeyframeRunner_RunAndSaveSkipsExistingKeyframes pins the resumability rule: a cut that
// already has a keyframe is not re-generated. Without it, any caller re-running a saved recipe
// (to generate video from it, to resume, to regenerate one cut) pays for every image again.
func TestCutKeyframeRunner_RunAndSaveSkipsExistingKeyframes(t *testing.T) {
	ctx := context.Background()

	t.Run("generates only the cuts missing a keyframe", func(t *testing.T) {
		generator := &capturingCutImageGenerator{}
		writer := newFakeWriter()
		recipe := &ports.VideoRecipe{
			ProjectTitle: "test",
			Cuts: []ports.Cut{
				keyframeCut(1, "gs://bucket/jobs/job-1/images/keyframe_1.png"),
				keyframeCut(2, ""),
				keyframeCut(3, "gs://bucket/jobs/job-1/images/keyframe_3.png"),
			},
		}

		updated, err := NewCutKeyframeRunner(generator, writer).RunAndSave(ctx, recipe, "gs://bucket/jobs/job-2/")
		if err != nil {
			t.Fatalf("RunAndSave() error = %v", err)
		}
		if len(generator.seen) != 1 {
			t.Fatalf("generator called %d times, want 1", len(generator.seen))
		}
		if got := generator.seen[0]; len(got) != 1 || got[0].CutIndex != 2 {
			t.Fatalf("generated cuts = %+v, want only cut 2", got)
		}
		// 既存のキーフレームは書き換えない。
		if updated.Cuts[0].KeyframeReference != "gs://bucket/jobs/job-1/images/keyframe_1.png" {
			t.Errorf("cut 1 keyframe = %q, want the existing reference untouched", updated.Cuts[0].KeyframeReference)
		}
		// 保存名はレシピ内の位置で決まる。詰めて keyframe_1.png にしてはいけない。
		if want := "keyframe_2"; !containsPathFragment(writer.writes, want) {
			t.Errorf("saved paths %v, want one containing %q (position-based numbering)", writerPaths(writer), want)
		}
		if containsPathFragment(writer.writes, "keyframe_1.png") {
			t.Errorf("saved paths %v unexpectedly wrote keyframe_1.png for cut 2", writerPaths(writer))
		}
	})

	t.Run("skips generation entirely when every cut has a keyframe", func(t *testing.T) {
		generator := &capturingCutImageGenerator{}
		writer := newFakeWriter()
		recipe := &ports.VideoRecipe{
			ProjectTitle: "test",
			Cuts: []ports.Cut{
				keyframeCut(1, "gs://bucket/jobs/job-1/images/keyframe_1.png"),
				keyframeCut(2, "gs://bucket/jobs/job-1/images/keyframe_2.png"),
			},
		}

		if _, err := NewCutKeyframeRunner(generator, writer).RunAndSave(ctx, recipe, "gs://bucket/jobs/job-2/"); err != nil {
			t.Fatalf("RunAndSave() error = %v", err)
		}
		if len(generator.seen) != 0 {
			t.Errorf("generator was called %d times, want 0", len(generator.seen))
		}
		// メタデータは生成をスキップしても必ず書く。呼び出し側はこのファイルを目印に
		// ジョブを見つけるため、書かないとジョブが存在しないように見える。
		if !containsPathFragment(writer.writes, "video_music_meta.json") {
			t.Errorf("saved paths %v, want the metadata to be written even with nothing generated", writerPaths(writer))
		}
	})

	t.Run("regenerates a cut whose keyframe reference was cleared", func(t *testing.T) {
		generator := &capturingCutImageGenerator{}
		writer := newFakeWriter()
		recipe := &ports.VideoRecipe{
			ProjectTitle: "test",
			Cuts:         []ports.Cut{keyframeCut(1, "")},
		}

		updated, err := NewCutKeyframeRunner(generator, writer).RunAndSave(ctx, recipe, "gs://bucket/jobs/job-1/")
		if err != nil {
			t.Fatalf("RunAndSave() error = %v", err)
		}
		if len(generator.seen) != 1 {
			t.Fatalf("generator called %d times, want 1 (clearing the reference means regenerate)", len(generator.seen))
		}
		if updated.Cuts[0].KeyframeReference == "" {
			t.Error("cut 1 keyframe reference was not set after generation")
		}
	})
}

func writerPaths(w *fakeWriter) []string {
	paths := make([]string, 0, len(w.writes))
	for path := range w.writes {
		paths = append(paths, path)
	}
	return paths
}

func containsPathFragment(writes map[string][]byte, fragment string) bool {
	for path := range writes {
		if strings.Contains(path, fragment) {
			return true
		}
	}
	return false
}

// TestCutKeyframeRunner_RunSkipsExistingKeyframes pins the same rule on Run that RunAndSave
// follows, with the alignment contract VideoTimelineRunner depends on: the returned slice
// matches recipe.Cuts position for position, and cuts that already had an image come back nil.
func TestCutKeyframeRunner_RunSkipsExistingKeyframes(t *testing.T) {
	ctx := context.Background()
	generator := &capturingCutImageGenerator{}
	recipe := &ports.VideoRecipe{
		ProjectTitle: "test",
		Cuts: []ports.Cut{
			keyframeCut(1, "gs://bucket/jobs/job-1/images/keyframe_1.png"),
			keyframeCut(2, ""),
			keyframeCut(3, "gs://bucket/jobs/job-1/images/keyframe_3.png"),
		},
	}

	images, err := NewCutKeyframeRunner(generator, newFakeWriter()).Run(ctx, recipe)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(images) != len(recipe.Cuts) {
		t.Fatalf("len(images) = %d, want %d (must stay aligned with the cuts)", len(images), len(recipe.Cuts))
	}
	if images[0] != nil || images[2] != nil {
		t.Error("cuts that already had a keyframe should come back nil, not a fresh image")
	}
	if images[1] == nil {
		t.Error("the cut without a keyframe was not generated")
	}
	if len(generator.seen) != 1 || len(generator.seen[0]) != 1 || generator.seen[0][0].CutIndex != 2 {
		t.Errorf("generated cuts = %+v, want only cut 2", generator.seen)
	}
}
