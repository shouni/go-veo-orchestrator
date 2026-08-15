package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-veo-orchestrator/ports"
	"github.com/shouni/go-veo-orchestrator/video"
)

// fakeWriter records every Write call so tests can assert on the saved paths/content.
// fakeWriter は remoteio.Writer のテストダブルです。
//
// mu は必須です。GenerateAndSave はカットごとに goroutine を起こし、その中で保存まで
// 行うため、Writer は複数の goroutine から同時に呼ばれます（注入する実装にも同じ
// 要件が掛かります。CutKeyframeRunner のドキュメント参照）。
type fakeWriter struct {
	mu     sync.Mutex
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
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writes[path] = data
	return nil
}

// writeCount は記録済みの書き込み数を返します（並行実行中でも安全に読めます）。
func (w *fakeWriter) writeCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.writes)
}

// fakeCutImageGenerator implements both ports.CutImageGenerator and cutImageEditor so tests
// can control EditAndSave's behavior independently of the real keyframe.Generator.
type fakeCutImageGenerator struct {
	editFunc func(ctx context.Context, cut video.Cut, editPrompt string) (*video.KeyframeImage, error)
}

func (f *fakeCutImageGenerator) GenerateCut(_ context.Context, _ video.Cut, _, _ int) (*video.KeyframeImage, error) {
	return nil, fmt.Errorf("not implemented")
}

func (f *fakeCutImageGenerator) EditCut(ctx context.Context, cut video.Cut, editPrompt string) (*video.KeyframeImage, error) {
	if f.editFunc != nil {
		return f.editFunc(ctx, cut, editPrompt)
	}
	return &video.KeyframeImage{Data: []byte("edited"), MimeType: "image/png"}, nil
}

// nonEditingCutImageGenerator implements ports.CutImageGenerator only, to exercise the
// "generator does not support editing" error path.
type nonEditingCutImageGenerator struct{}

func (nonEditingCutImageGenerator) GenerateCut(_ context.Context, _ video.Cut, _, _ int) (*video.KeyframeImage, error) {
	return nil, fmt.Errorf("not implemented")
}

// recordingCutImageGenerator implements ports.CutImageGenerator and records how many cuts
// it was asked to generate, so tests can drive GenerateAndSave independently of EditAndSave.
type recordingCutImageGenerator struct {
	images []*video.KeyframeImage
	err    error
	calls  int
}

func (g *recordingCutImageGenerator) GenerateCut(_ context.Context, _ video.Cut, index, _ int) (*video.KeyframeImage, error) {
	g.calls++
	if g.err != nil {
		return nil, g.err
	}
	if index-1 < len(g.images) {
		return g.images[index-1], nil
	}
	return &video.KeyframeImage{Data: []byte("img"), MimeType: "image/png"}, nil
}

func TestCutKeyframeRunner_EditAndSave(t *testing.T) {
	ctx := context.Background()

	t.Run("edits the single cut and saves keyframe + metadata", func(t *testing.T) {
		var captured struct {
			cut        video.Cut
			editPrompt string
		}
		gen := &fakeCutImageGenerator{
			editFunc: func(_ context.Context, cut video.Cut, editPrompt string) (*video.KeyframeImage, error) {
				captured.cut = cut
				captured.editPrompt = editPrompt
				return &video.KeyframeImage{Data: []byte("edited-bytes"), MimeType: "image/png"}, nil
			},
		}
		writer := newFakeWriter()
		r := NewCutKeyframeRunner(gen, writer)

		recipe := &video.Recipe{
			Cuts: []video.Cut{
				{CutIndex: 2, CharacterID: "zundamon", KeyframeResult: video.KeyframeResult{KeyframeReference: "gs://bucket/jobs/j1/images/keyframe_2.png"}},
			},
		}

		got, err := r.EditAndSave(ctx, recipe, 0, "腕には絆創膏を1〜2枚のみ", "gs://bucket/jobs/regen-1/regens/cut-2/")
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
		if writer.writeCount() != 2 {
			t.Fatalf("expected 2 writes (keyframe + metadata), got %d: %v", writer.writeCount(), writer.writes)
		}
	})

	t.Run("edits the targeted cut of a multi-cut recipe", func(t *testing.T) {
		var captured video.Cut
		gen := &fakeCutImageGenerator{
			editFunc: func(_ context.Context, cut video.Cut, _ string) (*video.KeyframeImage, error) {
				captured = cut
				return &video.KeyframeImage{Data: []byte("edited"), MimeType: "image/png"}, nil
			},
		}
		r := NewCutKeyframeRunner(gen, newFakeWriter())
		recipe := &video.Recipe{Cuts: []video.Cut{
			{CutIndex: 1, KeyframeResult: video.KeyframeResult{KeyframeReference: "gs://bucket/k1.png"}},
			{CutIndex: 2, KeyframeResult: video.KeyframeResult{KeyframeReference: "gs://bucket/k2.png"}},
		}}

		if _, err := r.EditAndSave(ctx, recipe, 1, "edit", "gs://bucket/out/"); err != nil {
			t.Fatalf("EditAndSave failed: %v", err)
		}
		if captured.CutIndex != 2 {
			t.Errorf("edited cut = %d, want the cut at position 1", captured.CutIndex)
		}
		if recipe.Cuts[0].KeyframeReference != "gs://bucket/k1.png" {
			t.Error("untouched cut's keyframe reference must not change")
		}
	})

	t.Run("errors when cut position is out of range", func(t *testing.T) {
		r := NewCutKeyframeRunner(&fakeCutImageGenerator{}, newFakeWriter())
		recipe := &video.Recipe{Cuts: []video.Cut{{CutIndex: 1}}}

		if _, err := r.EditAndSave(ctx, recipe, 5, "edit", "gs://bucket/out/"); err == nil {
			t.Fatal("expected error for out-of-range cut position")
		}
	})

	t.Run("errors when cut has no existing keyframe", func(t *testing.T) {
		r := NewCutKeyframeRunner(&fakeCutImageGenerator{}, newFakeWriter())
		recipe := &video.Recipe{Cuts: []video.Cut{{CutIndex: 1}}}

		if _, err := r.EditAndSave(ctx, recipe, 0, "edit", "gs://bucket/out/"); err == nil {
			t.Fatal("expected error for cut with no KeyframeReference")
		}
	})

	t.Run("errors when generator does not support editing", func(t *testing.T) {
		r := NewCutKeyframeRunner(nonEditingCutImageGenerator{}, newFakeWriter())
		recipe := &video.Recipe{Cuts: []video.Cut{{CutIndex: 1, KeyframeResult: video.KeyframeResult{KeyframeReference: "gs://bucket/k.png"}}}}

		_, err := r.EditAndSave(ctx, recipe, 0, "edit", "gs://bucket/out/")
		if !errors.Is(err, ports.ErrEditingNotSupported) {
			t.Fatalf("expected ErrEditingNotSupported, got %v", err)
		}
	})

	t.Run("returns ErrRecipeRequired for nil recipe", func(t *testing.T) {
		r := NewCutKeyframeRunner(&fakeCutImageGenerator{}, newFakeWriter())

		_, err := r.EditAndSave(ctx, nil, 0, "edit", "gs://bucket/out/")
		if !errors.Is(err, ports.ErrRecipeRequired) {
			t.Fatalf("expected ErrRecipeRequired, got %v", err)
		}
	})
}

func TestCutKeyframeRunner_RunAndSave(t *testing.T) {
	ctx := context.Background()

	t.Run("saves indexed keyframes and updated metadata", func(t *testing.T) {
		images := []*video.KeyframeImage{
			{Data: []byte("img-1"), MimeType: "image/png"},
			{Data: []byte("img-2"), MimeType: "image/png"},
		}
		gen := &recordingCutImageGenerator{images: images}
		writer := newFakeWriter()
		r := NewCutKeyframeRunner(gen, writer)
		recipe := &video.Recipe{Cuts: []video.Cut{{CutIndex: 1}, {CutIndex: 2}}}

		got, err := r.GenerateAndSave(ctx, recipe, "gs://bucket/jobs/j1/")
		if err != nil {
			t.Fatalf("GenerateAndSave() error = %v", err)
		}
		if got.Cuts[0].KeyframeReference == "" || got.Cuts[1].KeyframeReference == "" {
			t.Fatal("expected KeyframeReference to be set for all cuts")
		}
		if got.Cuts[0].KeyframeReference == got.Cuts[1].KeyframeReference {
			t.Fatalf("expected distinct indexed paths per cut, both = %q", got.Cuts[0].KeyframeReference)
		}
		if writer.writeCount() != 3 { // 2 keyframes + metadata
			t.Fatalf("expected 3 writes, got %d: %v", writer.writeCount(), writer.writes)
		}
	})

	t.Run("wraps generator failure", func(t *testing.T) {
		gen := &recordingCutImageGenerator{err: fmt.Errorf("upstream failure")}
		r := NewCutKeyframeRunner(gen, newFakeWriter())
		recipe := &video.Recipe{Cuts: []video.Cut{{CutIndex: 1}}}

		if _, err := r.GenerateAndSave(ctx, recipe, "gs://bucket/out/"); err == nil {
			t.Fatal("expected error when generator fails")
		}
	})

	t.Run("returns ErrRecipeRequired for nil recipe", func(t *testing.T) {
		r := NewCutKeyframeRunner(&recordingCutImageGenerator{}, newFakeWriter())

		_, err := r.GenerateAndSave(ctx, nil, "gs://bucket/out/")
		if !errors.Is(err, ports.ErrRecipeRequired) {
			t.Fatalf("expected ErrRecipeRequired, got %v", err)
		}
	})
}

// capturingCutImageGenerator records the cuts it was asked to generate, so tests can assert
// which cuts GenerateAndSave decided still needed a keyframe. Generation runs in parallel,
// so the recording is mutex-guarded and the order is not meaningful — assert on membership.
type capturingCutImageGenerator struct {
	mu    sync.Mutex
	seen  []video.Cut
	image func(cut video.Cut) *video.KeyframeImage
}

func (g *capturingCutImageGenerator) GenerateCut(_ context.Context, cut video.Cut, _, _ int) (*video.KeyframeImage, error) {
	g.mu.Lock()
	g.seen = append(g.seen, cut)
	g.mu.Unlock()

	if g.image != nil {
		return g.image(cut), nil
	}
	return &video.KeyframeImage{Data: []byte("generated"), MimeType: "image/png"}, nil
}

// seenCutIndexes returns the CutIndex values passed to GenerateCut, sorted so assertions do
// not depend on goroutine scheduling.
func (g *capturingCutImageGenerator) seenCutIndexes() []int {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]int, 0, len(g.seen))
	for _, cut := range g.seen {
		out = append(out, cut.CutIndex)
	}
	sort.Ints(out)
	return out
}

func keyframeCut(index int, keyframeReference string) video.Cut {
	return video.Cut{
		CutIndex:       index,
		VisualAnchor:   fmt.Sprintf("anchor %d", index),
		AudioSync:      video.AudioSync{DurationSec: 8},
		KeyframeResult: video.KeyframeResult{KeyframeReference: keyframeReference},
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
		recipe := &video.Recipe{
			ProjectTitle: "test",
			Cuts: []video.Cut{
				keyframeCut(1, "gs://bucket/jobs/job-1/images/keyframe_1.png"),
				keyframeCut(2, ""),
				keyframeCut(3, "gs://bucket/jobs/job-1/images/keyframe_3.png"),
			},
		}

		updated, err := NewCutKeyframeRunner(generator, writer).GenerateAndSave(ctx, recipe, "gs://bucket/jobs/job-2/")
		if err != nil {
			t.Fatalf("GenerateAndSave() error = %v", err)
		}
		if got := generator.seenCutIndexes(); len(got) != 1 || got[0] != 2 {
			t.Fatalf("generated cuts = %v, want only cut 2", got)
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
		recipe := &video.Recipe{
			ProjectTitle: "test",
			Cuts: []video.Cut{
				keyframeCut(1, "gs://bucket/jobs/job-1/images/keyframe_1.png"),
				keyframeCut(2, "gs://bucket/jobs/job-1/images/keyframe_2.png"),
			},
		}

		if _, err := NewCutKeyframeRunner(generator, writer).GenerateAndSave(ctx, recipe, "gs://bucket/jobs/job-2/"); err != nil {
			t.Fatalf("GenerateAndSave() error = %v", err)
		}
		if got := generator.seenCutIndexes(); len(got) != 0 {
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
		recipe := &video.Recipe{
			ProjectTitle: "test",
			Cuts:         []video.Cut{keyframeCut(1, "")},
		}

		updated, err := NewCutKeyframeRunner(generator, writer).GenerateAndSave(ctx, recipe, "gs://bucket/jobs/job-1/")
		if err != nil {
			t.Fatalf("GenerateAndSave() error = %v", err)
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

// TestCutKeyframeRunner_SavesEachKeyframeAsItIsGenerated pins the operational guarantee that
// replaced the old generate-all-then-save-all flow: a keyframe is written to storage and
// recorded on the recipe as soon as it is produced. Batching the writes meant that a crash
// mid-run (Cloud Run timeout, deploy, OOM) lost every generated — and already billed — image.
//
// It also pins that the seed each image was generated with lands on the cut, since the image
// itself never reaches the recipe and the video stage reuses that seed.
func TestCutKeyframeRunner_SavesEachKeyframeAsItIsGenerated(t *testing.T) {
	ctx := context.Background()
	writer := newFakeWriter()
	recipe := &video.Recipe{
		ProjectTitle: "test",
		Cuts:         []video.Cut{keyframeCut(1, ""), keyframeCut(2, "")},
	}

	// 既定の並列度は1なので、カット2の生成時点でカット1は必ず完了している。
	var firstSavedBeforeSecond bool
	generator := &capturingCutImageGenerator{
		image: func(cut video.Cut) *video.KeyframeImage {
			if cut.CutIndex == 2 {
				firstSavedBeforeSecond = recipe.Cuts[0].KeyframeReference != ""
			}
			return &video.KeyframeImage{
				Data: []byte("generated"), MimeType: "image/png", UsedSeed: int64(100 + cut.CutIndex),
			}
		},
	}

	updated, err := NewCutKeyframeRunner(generator, writer).GenerateAndSave(ctx, recipe, "gs://bucket/jobs/job-1/")
	if err != nil {
		t.Fatalf("GenerateAndSave() error = %v", err)
	}

	if !firstSavedBeforeSecond {
		t.Error("cut 1 must already be saved and referenced before cut 2 starts generating")
	}
	if updated.Cuts[0].KeyframeSeed != 101 || updated.Cuts[1].KeyframeSeed != 102 {
		t.Errorf("KeyframeSeed = %d/%d, want the seed each image was generated with",
			updated.Cuts[0].KeyframeSeed, updated.Cuts[1].KeyframeSeed)
	}
}

// TestCutKeyframeRunner_MaxConcurrency pins that WithMaxConcurrency actually bounds how many
// cuts are generated at once. Concurrency lives on this runner rather than the keyframe
// generator because the runner also owns the saving, and a cut's generate-and-save must stay
// inside one goroutine. Before it moved here the equivalent setting sat on ports.Config and was
// read by nothing at all, so the configured value was silently discarded.
func TestCutKeyframeRunner_MaxConcurrency(t *testing.T) {
	ctx := context.Background()

	for _, limit := range []int{1, 3} {
		t.Run(fmt.Sprintf("limit=%d", limit), func(t *testing.T) {
			var mu sync.Mutex
			inFlight, peak := 0, 0

			gen := &capturingCutImageGenerator{
				image: func(_ video.Cut) *video.KeyframeImage {
					mu.Lock()
					inFlight++
					if inFlight > peak {
						peak = inFlight
					}
					mu.Unlock()

					time.Sleep(20 * time.Millisecond)

					mu.Lock()
					inFlight--
					mu.Unlock()
					return &video.KeyframeImage{Data: []byte("img"), MimeType: "image/png"}
				},
			}

			cuts := make([]video.Cut, 8)
			for i := range cuts {
				cuts[i] = keyframeCut(i+1, "")
			}
			recipe := &video.Recipe{ProjectTitle: "concurrency", Cuts: cuts}

			r := NewCutKeyframeRunner(gen, newFakeWriter(), WithMaxConcurrency(limit))
			if _, err := r.GenerateAndSave(ctx, recipe, "gs://bucket/jobs/j1/"); err != nil {
				t.Fatalf("GenerateAndSave() error = %v", err)
			}

			mu.Lock()
			defer mu.Unlock()
			if peak > limit {
				t.Errorf("peak concurrency = %d, want <= %d", peak, limit)
			}
			if limit > 1 && peak < 2 {
				t.Errorf("peak concurrency = %d, want the cuts to actually overlap", peak)
			}
		})
	}
}

// TestCutKeyframeRunner_ParallelGenerationKeepsCutAlignment pins that each cut's reference and
// seed land on that cut, not on whichever finished first. Generation runs in parallel and the
// results are written straight into recipe.Cuts, so a position mix-up would silently attach
// cut 3's image to cut 1 — a failure that only shows up as the wrong picture in a finished video.
func TestCutKeyframeRunner_ParallelGenerationKeepsCutAlignment(t *testing.T) {
	ctx := context.Background()

	// 逆順に遅延させ、完了順を入力順とわざとずらす。
	gen := &capturingCutImageGenerator{
		image: func(cut video.Cut) *video.KeyframeImage {
			time.Sleep(time.Duration(5-cut.CutIndex) * 10 * time.Millisecond)
			return &video.KeyframeImage{
				Data: []byte("img"), MimeType: "image/png", UsedSeed: int64(1000 + cut.CutIndex),
			}
		},
	}

	cuts := make([]video.Cut, 4)
	for i := range cuts {
		cuts[i] = keyframeCut(i+1, "")
	}
	recipe := &video.Recipe{ProjectTitle: "alignment", Cuts: cuts}

	r := NewCutKeyframeRunner(gen, newFakeWriter(), WithMaxConcurrency(4))
	updated, err := r.GenerateAndSave(ctx, recipe, "gs://bucket/jobs/j1/")
	if err != nil {
		t.Fatalf("GenerateAndSave() error = %v", err)
	}

	for i, cut := range updated.Cuts {
		if want := int64(1000 + cut.CutIndex); cut.KeyframeSeed != want {
			t.Errorf("cut %d: KeyframeSeed = %d, want %d (result landed on the wrong cut)",
				cut.CutIndex, cut.KeyframeSeed, want)
		}
		// 保存名はレシピ内の位置で決まる。詰めたり入れ替えたりしてはいけない。
		if want := fmt.Sprintf("keyframe_%d", i+1); !strings.Contains(cut.KeyframeReference, want) {
			t.Errorf("cut %d: reference %q, want it to contain %q", cut.CutIndex, cut.KeyframeReference, want)
		}
	}
}
