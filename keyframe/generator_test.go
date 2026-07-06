package keyframe

import (
	"context"
	"sync"
	"testing"
	"time"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	characterkit "github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-veo-orchestrator/ports"
)

// --- Mocks ---

// mockImageGenerator は Generator.Execute からの並行呼び出しを受けるため、
// generateCount への読み書きを mu で保護しています。
type mockImageGenerator struct {
	mu            sync.Mutex
	generateCount int
	generateFunc  func(ctx context.Context, req imagePorts.SingleImageRequest) (*imagePorts.ImageResponse, error)
	editFunc      func(ctx context.Context, req imagePorts.EditImageRequest) (*imagePorts.ImageResponse, error)
}

func (m *mockImageGenerator) GenerateSingleImage(ctx context.Context, req imagePorts.SingleImageRequest) (*imagePorts.ImageResponse, error) {
	m.mu.Lock()
	m.generateCount++
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

func (m *mockImageGenerator) EditImage(ctx context.Context, req imagePorts.EditImageRequest) (*imagePorts.ImageResponse, error) {
	if m.editFunc != nil {
		return m.editFunc(ctx, req)
	}
	var s int64
	if req.Seed != nil {
		s = *req.Seed
	}
	return &imagePorts.ImageResponse{Data: []byte("fake-edited-keyframe-image"), UsedSeed: s}, nil
}

type mockImagePrompt struct{}

func (m *mockImagePrompt) BuildCut(cut ports.Cut, _ *characterkit.Character) (string, string) {
	return cut.VisualAnchor, "system"
}

// --- Tests ---

func TestGenerator_Execute(t *testing.T) {
	ctx := context.Background()

	// 1. 依存関係のセットアップ
	assetMgr := &mockAssetManager{}
	backend := &mockBackend{isVertex: false}

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
	composer, _ := NewComposer(assetMgr, backend, cm)

	genMock := &mockImageGenerator{}
	pbMock := &mockImagePrompt{}

	// 2. Generator の作成 (高速化設定)
	generator := NewGenerator(
		composer,
		genMock,
		pbMock,
		"gemini-2.0-flash",
		func(g *Generator) {
			g.maxConcurrency = 5
			g.rateInterval = 1 * time.Microsecond
			g.rateBurst = 100
		},
	)

	t.Run("Parallel Generation and Individual Seeds", func(t *testing.T) {
		cuts := []ports.Cut{
			{CharacterID: "zundamon", Dialogue: "こんにちはなのだ！"},
			{CharacterID: "metan", Dialogue: "ごきげんよう。"},
			{CharacterID: "unknown", Dialogue: "誰かしら？"}, // Defaultのmetanが使われるはず
		}

		// リクエストされた Seed を記録するためのスライス
		capturedSeeds := make([]int64, len(cuts))
		genMock.generateFunc = func(_ context.Context, req imagePorts.SingleImageRequest) (*imagePorts.ImageResponse, error) {
			// どのパネルのリクエストか特定するのが難しいため、
			// 呼ばれた順ではなく最終的な Seed 値を検証
			return &imagePorts.ImageResponse{UsedSeed: *req.Seed}, nil
		}

		res, err := generator.Execute(ctx, cuts)
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
		res, err := generator.Execute(ctx, []ports.Cut{})
		if err != nil {
			t.Fatalf("Execute failed on empty: %v", err)
		}
		if res != nil {
			t.Error("Expected nil response for empty keyframes")
		}
	})

	t.Run("Vertex AI Bypass in Keyframe Generation", func(t *testing.T) {
		// Vertex モードでは File API へのアップロードをスキップして直接生成に回る
		backend.isVertex = true
		genMock.generateCount = 0

		cuts := []ports.Cut{{CharacterID: "zundamon"}}

		_, err := generator.Execute(ctx, cuts)
		if err != nil {
			t.Fatal(err)
		}

		if genMock.generateCount != 1 {
			t.Errorf("Expected 1 generation call, got %d", genMock.generateCount)
		}
		// mockAssetManager.uploadCount が増えていないことを確認したいが、
		// PrepareCharacterResources 内の挙動に依存するためここでは生成が成功することを確認
	})
}

func TestGenerator_EditCut(t *testing.T) {
	ctx := context.Background()

	assetMgr := &mockAssetManager{}
	backend := &mockBackend{isVertex: false}
	zundamonSeed := int64(10001)
	cm := mustNewCharacters(t, []characterkit.Character{
		{ID: "zundamon", Name: "ずんだもん", VisualCues: []string{"green hair"}, Seed: &zundamonSeed, ReferenceURL: "gs://bucket/zunda.png", IsDefault: true},
	})
	composer, _ := NewComposer(assetMgr, backend, cm)
	genMock := &mockImageGenerator{}
	pbMock := &mockImagePrompt{}
	generator := NewGenerator(composer, genMock, pbMock, "gemini-2.0-flash",
		WithEditModel("imagen-3.0-capability-001"),
		func(g *Generator) {
			g.maxConcurrency = 5
			g.rateInterval = 1 * time.Microsecond
			g.rateBurst = 100
		},
	)

	t.Run("Uses existing keyframe as source and character seed", func(t *testing.T) {
		var captured imagePorts.EditImageRequest
		genMock.editFunc = func(_ context.Context, req imagePorts.EditImageRequest) (*imagePorts.ImageResponse, error) {
			captured = req
			return &imagePorts.ImageResponse{Data: []byte("edited"), UsedSeed: *req.Seed}, nil
		}

		cut := ports.Cut{CutIndex: 2, CharacterID: "zundamon", KeyframeReference: "gs://bucket/jobs/j1/images/keyframe_2.png"}
		resp, err := generator.EditCut(ctx, cut, "腕には絆創膏を1〜2枚のみにしてください")
		if err != nil {
			t.Fatalf("EditCut failed: %v", err)
		}
		if string(resp.Data) != "edited" {
			t.Errorf("unexpected response data: %q", resp.Data)
		}
		if captured.Image.ReferenceURL != cut.KeyframeReference {
			t.Errorf("edit request image = %q, want %q", captured.Image.ReferenceURL, cut.KeyframeReference)
		}
		if captured.EditPrompt != "腕には絆創膏を1〜2枚のみにしてください" {
			t.Errorf("edit request prompt = %q", captured.EditPrompt)
		}
		if captured.Seed == nil || *captured.Seed != zundamonSeed {
			t.Errorf("edit request seed = %v, want %d", captured.Seed, zundamonSeed)
		}
		if captured.Model != "imagen-3.0-capability-001" {
			t.Errorf("edit request model = %q, want the configured edit model (not the generation model)", captured.Model)
		}
	})

	t.Run("Errors when cut has no existing keyframe", func(t *testing.T) {
		cut := ports.Cut{CutIndex: 1, CharacterID: "zundamon"}
		if _, err := generator.EditCut(ctx, cut, "edit"); err == nil {
			t.Fatal("expected error for cut with no KeyframeReference")
		}
	})

	t.Run("Errors when no edit model is configured", func(t *testing.T) {
		g := NewGenerator(composer, genMock, pbMock, "gemini-2.0-flash", func(g *Generator) {
			g.maxConcurrency = 5
			g.rateInterval = 1 * time.Microsecond
			g.rateBurst = 100
		})
		cut := ports.Cut{CutIndex: 1, CharacterID: "zundamon", KeyframeReference: "gs://bucket/keyframe.png"}
		if _, err := g.EditCut(ctx, cut, "edit"); err == nil {
			t.Fatal("expected error when no edit model is configured")
		}
	})

	t.Run("Errors when character is unknown and no default exists", func(t *testing.T) {
		emptyComposer, _ := NewComposer(assetMgr, backend, mustNewCharacters(t, nil))
		g := NewGenerator(emptyComposer, genMock, pbMock, "gemini-2.0-flash", func(g *Generator) {
			g.maxConcurrency = 5
			g.rateInterval = 1 * time.Microsecond
			g.rateBurst = 100
		})
		cut := ports.Cut{CutIndex: 1, CharacterID: "no-such-character", KeyframeReference: "gs://bucket/keyframe.png"}
		if _, err := g.EditCut(ctx, cut, "edit"); err == nil {
			t.Fatal("expected error for unknown character with no default")
		}
	})
}
