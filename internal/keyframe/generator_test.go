package keyframe

import (
	"context"
	"errors"
	"sync"
	"testing"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	characterkit "github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-veo-orchestrator/video"
)

// --- Mocks ---

// mockImageGenerator は gemini-image-kit の ImageGenerator を模したテストダブルです。
// このパッケージの生成は逐次ですが、runner 側のテストからも並列に叩かれるため
// mu で保護します。
type mockImageGenerator struct {
	mu            sync.Mutex
	generateCount int
	lastReq       imagePorts.ImageRequest
	generateFunc  func(ctx context.Context, req imagePorts.ImageRequest) (*imagePorts.ImageResponse, error)
}

func (m *mockImageGenerator) Generate(ctx context.Context, req imagePorts.ImageRequest) (*imagePorts.ImageResponse, error) {
	m.mu.Lock()
	m.generateCount++
	m.lastReq = req
	m.mu.Unlock()
	if m.generateFunc != nil {
		return m.generateFunc(ctx, req)
	}
	var s int64
	if req.Seed != nil {
		s = *req.Seed
	}
	return &imagePorts.ImageResponse{Data: []byte("fake-keyframe-image"), UsedSeed: s}, nil
}

type mockImagePrompt struct{}

func (m *mockImagePrompt) BuildCut(cut video.Cut, _ *characterkit.Character) (string, string) {
	return cut.VisualAnchor, "system"
}

func (m *mockImagePrompt) BuildEdit(_ video.Cut, _ *characterkit.Character, editPrompt string) (string, string) {
	return editPrompt, "edit-system"
}

// execAll はカット列をまとめて生成するテスト用ヘルパーです。本番の並列実行と保存は
// runner.CutKeyframeRunner が持つため、ここでは逐次で足ります。
func execAll(ctx context.Context, t *testing.T, g *Generator, cuts []video.Cut) ([]*video.KeyframeImage, error) {
	t.Helper()
	images := make([]*video.KeyframeImage, len(cuts))
	var errs []error
	for i, cut := range cuts {
		img, err := g.GenerateCut(ctx, cut, i+1, len(cuts))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		images[i] = img
	}
	return images, errors.Join(errs...)
}

// --- Tests ---

func TestGenerator_GenerateCut(t *testing.T) {
	ctx := context.Background()

	// 1. 依存関係のセットアップ

	// 異なる Seed 値を持つキャラクターを用意
	zundamonSeed := int64(10001)
	metanSeed := int64(20002)
	cm := mustNewCharacters(t, []characterkit.Character{
		{
			ID:           "zundamon",
			Name:         "ずんだもん",
			VisualCues:   []string{"green hair"},
			Seed:         &zundamonSeed,
			ReferenceURL: "gs://bucket/zunda.png",
		},
		{
			ID:           "metan",
			Name:         "めたん",
			VisualCues:   []string{"purple hair"},
			Seed:         &metanSeed,
			ReferenceURL: "gs://bucket/metan.png",
			IsDefault:    true, // 指定なしの場合のデフォルト
		},
	})

	genMock := &mockImageGenerator{}
	pbMock := &mockImagePrompt{}

	// 2. Generator の作成 (高速化設定)
	generator := NewGenerator(
		cm,
		genMock,
		pbMock,
		"gemini-2.0-flash",
	)

	t.Run("Sequential Generation and Individual Seeds", func(t *testing.T) {
		cuts := []video.Cut{
			{CharacterID: "zundamon", Dialogue: "こんにちはなのだ！"},
			{CharacterID: "metan", Dialogue: "ごきげんよう。"},
			{CharacterID: "unknown", Dialogue: "誰かしら？"}, // Defaultのmetanが使われるはず
		}

		// リクエストされた Seed を記録するためのスライス
		capturedSeeds := make([]int64, len(cuts))
		genMock.generateFunc = func(_ context.Context, req imagePorts.ImageRequest) (*imagePorts.ImageResponse, error) {
			return &imagePorts.ImageResponse{UsedSeed: *req.Seed}, nil
		}

		res, err := execAll(ctx, t, generator, cuts)
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}

		if len(res) != 3 {
			t.Errorf("Expected 3 images, got %d", len(res))
		}

		for i, r := range res {
			capturedSeeds[i] = r.UsedSeed
		}

		// インデックスごとの Seed 検証
		if capturedSeeds[0] != 10001 {
			t.Errorf("Keyframe 0 (zundamon) expected seed 10001, got %d", capturedSeeds[0])
		}
		if capturedSeeds[1] != 20002 {
			t.Errorf("Keyframe 1 (metan) expected seed 20002, got %d", capturedSeeds[1])
		}
		if capturedSeeds[2] != 20002 {
			t.Errorf("Keyframe 2 (unknown->metan) expected seed 20002, got %d", capturedSeeds[2])
		}
	})

	t.Run("Empty Keyframes Handling", func(t *testing.T) {
		res, err := execAll(ctx, t, generator, []video.Cut{})
		if err != nil {
			t.Fatalf("Execute failed on empty: %v", err)
		}
		if len(res) != 0 {
			t.Errorf("Expected no images for empty keyframes, got %d", len(res))
		}
	})

	// 参照は gs:// URI のまま渡り、Vertex AI が直接解決します（GCSResolver）。
	// ここでは「参照元 URL をそのまま渡していること」だけを見ます。
	t.Run("参照は解決せずそのまま渡す", func(t *testing.T) {
		genMock.generateCount = 0

		if _, err := execAll(ctx, t, generator, []video.Cut{{CharacterID: "zundamon"}}); err != nil {
			t.Fatal(err)
		}

		if genMock.generateCount != 1 {
			t.Errorf("Expected 1 generation call, got %d", genMock.generateCount)
		}
		if got := genMock.lastReq.Images[0].ReferenceURL; got != "gs://bucket/zunda.png" {
			t.Errorf("ReferenceURL = %q, want the character reference", got)
		}
	})
}

// TestGenerator_AspectRatio pins that the generator holds no art-direction default: what the
// caller passes is what gets sent, and passing nothing sends nothing. ports.Config requires
// KeyframeAspectRatio precisely so an empty value never reaches here in practice.
func TestGenerator_AspectRatio(t *testing.T) {
	ctx := context.Background()
	zundamonSeed := int64(10001)
	cm := mustNewCharacters(t, []characterkit.Character{
		{ID: "zundamon", Name: "ずんだもん", VisualCues: []string{"green hair"}, Seed: &zundamonSeed, ReferenceURL: "gs://bucket/zunda.png", IsDefault: true},
	})
	cuts := []video.Cut{{CharacterID: "zundamon"}}

	captureAspectRatio := func(opts ...Option) string {
		genMock := &mockImageGenerator{}
		g := NewGenerator(cm, genMock, &mockImagePrompt{}, "gemini-2.0-flash", opts...)
		var captured string
		genMock.generateFunc = func(_ context.Context, req imagePorts.ImageRequest) (*imagePorts.ImageResponse, error) {
			captured = req.AspectRatio
			return &imagePorts.ImageResponse{}, nil
		}
		if _, err := execAll(ctx, t, g, cuts); err != nil {
			t.Fatalf("Execute failed: %v", err)
		}
		return captured
	}

	t.Run("sends nothing when the caller sets nothing", func(t *testing.T) {
		if got := captureAspectRatio(); got != "" {
			t.Errorf("AspectRatio = %q, want empty (the kit holds no default)", got)
		}
	})

	t.Run("sends what the caller passed", func(t *testing.T) {
		if got := captureAspectRatio(WithAspectRatio("9:16")); got != "9:16" {
			t.Errorf("AspectRatio = %q, want %q", got, "9:16")
		}
	})
}

// TestGenerator_ReferenceURLPerAspectRatio verifies that buildImageRequest picks the character's
// aspect-ratio-specific reference image (ReferenceURLs) when one matches the generator's
// aspectRatio, falling back to ReferenceURL when no entry matches or none are configured.
func TestGenerator_ReferenceURLPerAspectRatio(t *testing.T) {
	ctx := context.Background()
	cm := mustNewCharacters(t, []characterkit.Character{
		{
			ID:           "tsumugi",
			Name:         "つむぎ",
			VisualCues:   []string{"orange hair"},
			ReferenceURL: "gs://bucket/tsumugi-16x9.png",
			ReferenceURLs: map[string]string{
				"9:16": "gs://bucket/tsumugi-9x16.png",
			},
			IsDefault: true,
		},
	})
	cuts := []video.Cut{{CharacterID: "tsumugi"}}

	newGenerator := func(opts ...Option) (*Generator, *mockImageGenerator) {
		genMock := &mockImageGenerator{}
		pbMock := &mockImagePrompt{}
		g := NewGenerator(cm, genMock, pbMock, "gemini-2.0-flash", opts...)
		return g, genMock
	}

	t.Run("uses aspect-ratio-specific entry when present", func(t *testing.T) {
		g, genMock := newGenerator(WithAspectRatio("9:16"))
		var captured string
		genMock.generateFunc = func(_ context.Context, req imagePorts.ImageRequest) (*imagePorts.ImageResponse, error) {
			captured = req.Images[0].ReferenceURL
			return &imagePorts.ImageResponse{}, nil
		}
		if _, err := execAll(ctx, t, g, cuts); err != nil {
			t.Fatalf("Execute failed: %v", err)
		}
		if captured != "gs://bucket/tsumugi-9x16.png" {
			t.Errorf("Image.ReferenceURL = %q, want the 9:16 entry", captured)
		}
	})

	t.Run("falls back to ReferenceURL when aspect ratio has no entry", func(t *testing.T) {
		g, genMock := newGenerator(WithAspectRatio("16:9"))
		var captured string
		genMock.generateFunc = func(_ context.Context, req imagePorts.ImageRequest) (*imagePorts.ImageResponse, error) {
			captured = req.Images[0].ReferenceURL
			return &imagePorts.ImageResponse{}, nil
		}
		if _, err := execAll(ctx, t, g, cuts); err != nil {
			t.Fatalf("Execute failed: %v", err)
		}
		if captured != "gs://bucket/tsumugi-16x9.png" {
			t.Errorf("Image.ReferenceURL = %q, want the ReferenceURL fallback", captured)
		}
	})
}

func TestGenerator_EditCut(t *testing.T) {
	ctx := context.Background()

	zundamonSeed := int64(10001)
	cm := mustNewCharacters(t, []characterkit.Character{
		{ID: "zundamon", Name: "ずんだもん", VisualCues: []string{"green hair"}, Seed: &zundamonSeed, ReferenceURL: "gs://bucket/zunda.png", IsDefault: true},
	})
	genMock := &mockImageGenerator{}
	pbMock := &mockImagePrompt{}
	generator := NewGenerator(cm, genMock, pbMock, "gemini-2.0-flash")

	t.Run("Uses existing keyframe as source, generation model, and character seed", func(t *testing.T) {
		var captured imagePorts.ImageRequest
		genMock.generateFunc = func(_ context.Context, req imagePorts.ImageRequest) (*imagePorts.ImageResponse, error) {
			captured = req
			return &imagePorts.ImageResponse{Data: []byte("edited"), UsedSeed: *req.Seed}, nil
		}

		cut := video.Cut{CutIndex: 2, CharacterID: "zundamon", KeyframeResult: video.KeyframeResult{KeyframeReference: "gs://bucket/jobs/j1/images/keyframe_2.png"}}
		resp, err := generator.EditCut(ctx, cut, "腕には絆創膏を1〜2枚のみにしてください")
		if err != nil {
			t.Fatalf("EditCut failed: %v", err)
		}
		if string(resp.Data) != "edited" {
			t.Errorf("unexpected response data: %q", resp.Data)
		}
		if captured.Images[0].ReferenceURL != cut.KeyframeReference {
			t.Errorf("edit request image = %q, want %q", captured.Images[0].ReferenceURL, cut.KeyframeReference)
		}
		if captured.Prompt != "腕には絆創膏を1〜2枚のみにしてください" {
			t.Errorf("edit request prompt = %q", captured.Prompt)
		}
		if captured.Seed == nil || *captured.Seed != zundamonSeed {
			t.Errorf("edit request seed = %v, want %d", captured.Seed, zundamonSeed)
		}
		if captured.Model != "gemini-2.0-flash" {
			t.Errorf("edit request model = %q, want the same conversational model used for generation", captured.Model)
		}
	})

	t.Run("Errors when cut has no existing keyframe", func(t *testing.T) {
		cut := video.Cut{CutIndex: 1, CharacterID: "zundamon"}
		if _, err := generator.EditCut(ctx, cut, "edit"); err == nil {
			t.Fatal("expected error for cut with no KeyframeReference")
		}
	})

	t.Run("Errors when character is unknown and no default exists", func(t *testing.T) {
		noDefault := mustNewCharacters(t, []characterkit.Character{
			{ID: "zundamon", Name: "ずんだもん", VisualCues: []string{"green hair"}, ReferenceURL: "gs://bucket/zunda.png"},
		})
		g := NewGenerator(noDefault, genMock, pbMock, "gemini-2.0-flash")
		cut := video.Cut{CutIndex: 1, CharacterID: "no-such-character", KeyframeResult: video.KeyframeResult{KeyframeReference: "gs://bucket/keyframe.png"}}
		if _, err := g.EditCut(ctx, cut, "edit"); err == nil {
			t.Fatal("expected error for unknown character with no default")
		}
	})
}
