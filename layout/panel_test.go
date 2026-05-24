package layout

import (
	"context"
	"testing"
	"time"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	"github.com/shouni/go-veo-orchestrator/ports"
)

// --- Mocks ---

type mockPanelImageGenerator struct {
	generateCount int
	generateFunc  func(ctx context.Context, req imagePorts.SingleImageRequest) (*imagePorts.ImageResponse, error)
}

func (m *mockPanelImageGenerator) GenerateSingleImage(ctx context.Context, req imagePorts.SingleImageRequest) (*imagePorts.ImageResponse, error) {
	m.generateCount++
	if m.generateFunc != nil {
		return m.generateFunc(ctx, req)
	}
	var s int64
	if req.Seed != nil {
		s = *req.Seed
	}
	return &imagePorts.ImageResponse{Data: []byte("fake-panel-image"), UsedSeed: s}, nil
}

type mockImagePrompt struct{}

func (m *mockImagePrompt) BuildPanel(panel ports.Panel, char *ports.Character) (string, string) {
	return panel.VisualAnchor, "system"
}

// --- Tests ---

func TestPanelGenerator_Execute(t *testing.T) {
	ctx := context.Background()

	// 1. 依存関係のセットアップ
	assetMgr := &mockAssetManager{}
	backend := &mockBackend{isVertex: false}

	// 異なる Seed 値を持つキャラクターを用意
	cm := ports.CharactersMap{
		"zundamon": ports.Character{
			ID:           "zundamon",
			Name:         "ずんだもん",
			Seed:         10001,
			ReferenceURL: "gs://bucket/zunda.png",
		},
		"metan": ports.Character{
			ID:           "metan",
			Name:         "めたん",
			Seed:         20002,
			ReferenceURL: "gs://bucket/metan.png",
			IsDefault:    true, // 指定なしの場合のデフォルト
		},
	}
	composer, _ := NewMangaComposer(assetMgr, backend, cm)

	genMock := &mockPanelImageGenerator{}
	pbMock := &mockImagePrompt{}

	// 2. Generator の作成 (高速化設定)
	generator := NewPanelGenerator(
		composer,
		genMock,
		pbMock,
		"gemini-2.0-flash",
		func(g *PanelGenerator) {
			g.maxConcurrency = 5
			g.rateInterval = 1 * time.Microsecond
			g.rateBurst = 100
		},
	)

	t.Run("Parallel Generation and Individual Seeds", func(t *testing.T) {
		panels := []ports.Panel{
			{SpeakerID: "zundamon", Dialogue: "こんにちはなのだ！"},
			{SpeakerID: "metan", Dialogue: "ごきげんよう。"},
			{SpeakerID: "unknown", Dialogue: "誰かしら？"}, // Defaultのmetanが使われるはず
		}

		// リクエストされた Seed を記録するためのスライス
		capturedSeeds := make([]int64, len(panels))
		genMock.generateFunc = func(ctx context.Context, req imagePorts.SingleImageRequest) (*imagePorts.ImageResponse, error) {
			// どのパネルのリクエストか特定するのが難しいため、
			// 呼ばれた順ではなく最終的な Seed 値を検証
			return &imagePorts.ImageResponse{UsedSeed: *req.Seed}, nil
		}

		res, err := generator.Execute(ctx, panels)
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
			t.Errorf("Panel 0 (zundamon) expected seed 10001, got %d", capturedSeeds[0])
		}
		if capturedSeeds[1] != 20002 {
			t.Errorf("Panel 1 (metan) expected seed 20002, got %d", capturedSeeds[1])
		}
		if capturedSeeds[2] != 20002 {
			t.Errorf("Panel 2 (unknown->metan) expected seed 20002, got %d", capturedSeeds[2])
		}
	})

	t.Run("Empty Panels Handling", func(t *testing.T) {
		res, err := generator.Execute(ctx, []ports.Panel{})
		if err != nil {
			t.Fatalf("Execute failed on empty: %v", err)
		}
		if res != nil {
			t.Error("Expected nil response for empty panels")
		}
	})

	t.Run("Vertex AI Bypass in Panel Generation", func(t *testing.T) {
		// Vertex モードでは File API へのアップロードをスキップして直接生成に回る
		backend.isVertex = true
		genMock.generateCount = 0

		panels := []ports.Panel{{SpeakerID: "zundamon"}}

		_, err := generator.Execute(ctx, panels)
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
