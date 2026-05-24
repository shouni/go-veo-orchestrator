package layout

import (
	"context"
	"testing"
	"time"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	"github.com/shouni/go-veo-orchestrator/ports"
)

// --- Mocks ---

type mockPageImageGenerator struct {
	generateCount int
	generateFunc  func(ctx context.Context, req imagePorts.ImageFusionRequest) (*imagePorts.ImageResponse, error)
}

func (m *mockPageImageGenerator) GenerateFusedImage(ctx context.Context, req imagePorts.ImageFusionRequest) (*imagePorts.ImageResponse, error) {
	m.generateCount++
	if m.generateFunc != nil {
		return m.generateFunc(ctx, req)
	}
	var s int64
	if req.Seed != nil {
		s = *req.Seed
	}
	return &imagePorts.ImageResponse{Data: []byte("fake-image"), UsedSeed: s}, nil
}

type mockImagePrompt struct{}

func (m *mockImagePrompt) BuildPanel(panel ports.Panel, char *ports.Character) (string, string) {
	return "user-prompt", "system-prompt"
}

func (m *mockImagePrompt) BuildPage(panels []ports.Panel, rm *ports.ResourceMap) (string, string) {
	return "page-user-prompt", "page-system-prompt"
}

// --- Tests ---

func TestPageGenerator_Execute(t *testing.T) {
	ctx := context.Background()

	// 1. 依存関係のセットアップ
	assetMgr := &mockAssetManager{}
	backend := &mockBackend{isVertex: false}

	cm := ports.CharactersMap{
		"zundamon": ports.Character{
			ID:           "zundamon",
			Name:         "ずんだもん",
			Seed:         12345,
			ReferenceURL: "gs://bucket/zunda.png",
		},
	}
	composer, _ := NewMangaComposer(assetMgr, backend, cm)

	genMock := &mockPageImageGenerator{}
	pbMock := &mockImagePrompt{}

	// 2. Generator の作成 (テスト高速化設定を注入)
	maxPanels := 2
	generator := NewPageGenerator(
		composer,
		genMock,
		pbMock,
		"gemini-2.0-flash",
		func(g *PageGenerator) {
			g.maxPanelsPerPage = maxPanels
			g.maxConcurrency = 5
			// 【重要】レート制限による待ち時間をほぼゼロにしてテストを高速化
			g.rateInterval = 1 * time.Microsecond
			g.rateBurst = 100
		},
	)

	t.Run("Chunking and Parallel Execution", func(t *testing.T) {
		manga := &ports.MangaResponse{
			Title: "Test Manga",
			Panels: []ports.Panel{
				{SpeakerID: "zundamon", Dialogue: "P1"},
				{SpeakerID: "zundamon", Dialogue: "P2"},
				{SpeakerID: "zundamon", Dialogue: "P3"},
				{SpeakerID: "zundamon", Dialogue: "P4"},
				{SpeakerID: "zundamon", Dialogue: "P5"},
			},
		}

		start := time.Now()
		responses, err := generator.Execute(ctx, manga)
		duration := time.Since(start)

		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}

		// 5パネル / 1ページ2枚制限 = 3ページ
		expectedPages := 3
		if len(responses) != expectedPages {
			t.Errorf("Expected %d pages, got %d", expectedPages, len(responses))
		}

		// 並列実行とレート制限解除が効いていれば、非常に短時間で終わるはず
		t.Logf("Execution duration: %v", duration)
		if duration > 1*time.Second {
			t.Errorf("Test took too long (%v), rate limiter might not be optimized", duration)
		}
	})

	t.Run("Seed Determination Logic", func(t *testing.T) {
		genMock.generateCount = 0
		manga := &ports.MangaResponse{
			Title: "Seed Test",
			Panels: []ports.Panel{
				{SpeakerID: "zundamon", Dialogue: "Hello"},
			},
		}

		var capturedSeed int64
		genMock.generateFunc = func(ctx context.Context, req imagePorts.ImageFusionRequest) (*imagePorts.ImageResponse, error) {
			if req.Seed != nil {
				capturedSeed = *req.Seed
			}
			return &imagePorts.ImageResponse{}, nil
		}

		_, err := generator.Execute(ctx, manga)
		if err != nil {
			t.Fatal(err)
		}

		// キャラクターの Seed 12345 がリクエストに反映されているか
		if capturedSeed != 12345 {
			t.Errorf("Expected seed 12345, got %d", capturedSeed)
		}
	})
}

func TestChunkPanels(t *testing.T) {
	panels := make([]ports.Panel, 5)
	got := chunkPanels(panels, 2)
	if len(got) != 3 {
		t.Errorf("Expected 3 chunks, got %d", len(got))
	}
	if len(got[2]) != 1 {
		t.Errorf("Expected last chunk size 1, got %d", len(got[2]))
	}
}
