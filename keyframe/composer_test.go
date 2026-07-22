package keyframe

import (
	"context"
	"sync/atomic"
	"testing"

	characterkit "github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-veo-orchestrator/ports"
)

// --- Mocks ---

type mockAssetManager struct {
	uploadCount int32
	deleteCount int32
	uploadFunc  func(ctx context.Context, refURL string) (string, error)
}

func (m *mockAssetManager) UploadFile(ctx context.Context, refURL string) (string, error) {
	atomic.AddInt32(&m.uploadCount, 1)
	if m.uploadFunc != nil {
		return m.uploadFunc(ctx, refURL)
	}
	return "https://file-api.google.com/" + refURL, nil
}

func (m *mockAssetManager) DeleteFile(_ context.Context, _ string) error {
	atomic.AddInt32(&m.deleteCount, 1)
	return nil
}

type mockBackend struct {
	isVertex bool
}

func (m *mockBackend) IsVertexAI() bool { return m.isVertex }

func mustNewCharacters(t *testing.T, list []characterkit.Character) *characterkit.Characters {
	t.Helper()

	chars, err := characterkit.NewCharacters(list)
	if err != nil {
		t.Fatalf("NewCharacters failed: %v", err)
	}

	return chars
}

// --- Tests ---

func TestComposer_PrepareCharacterResources(t *testing.T) {
	ctx := context.Background()
	assetMgr := &mockAssetManager{}
	backend := &mockBackend{isVertex: false}

	cm := mustNewCharacters(t, []characterkit.Character{
		{
			ID:           "zundamon",
			Name:         "ずんだもん",
			VisualCues:   []string{"green hair"},
			ReferenceURL: "gs://bucket/zunda.png",
		},
		{
			ID:           "metan",
			Name:         "めたん",
			VisualCues:   []string{"purple hair"},
			ReferenceURL: "gs://bucket/metan.png",
			IsDefault:    true,
		},
	})

	mc, _ := NewComposer(assetMgr, backend, cm)

	cuts := []ports.Cut{
		{CharacterID: "zundamon"},
		{CharacterID: "unknown"}, // default (metan) が使用される
	}

	err := mc.PrepareCharacterResources(ctx, cuts)
	if err != nil {
		t.Fatalf("PrepareCharacterResources failed: %v", err)
	}

	if uri := mc.GetResourceURI("gs://bucket/zunda.png"); uri == "" {
		t.Error("zundamon resource not cached")
	}
	if uri := mc.GetResourceURI("gs://bucket/metan.png"); uri == "" {
		t.Error("default character (metan) resource not cached")
	}

	if assetMgr.uploadCount != 2 {
		t.Errorf("Expected 2 uploads, got %d", assetMgr.uploadCount)
	}
}

// TestComposer_PrepareCharacterResourcesUploadsAllAspectRatioVariants verifies that
// PrepareCharacterResources uploads both ReferenceURL and every ReferenceURLs entry, and that
// GetResourceURI resolves each one independently by URL.
func TestComposer_PrepareCharacterResourcesUploadsAllAspectRatioVariants(t *testing.T) {
	ctx := context.Background()
	assetMgr := &mockAssetManager{}
	backend := &mockBackend{isVertex: false}

	cm := mustNewCharacters(t, []characterkit.Character{
		{
			ID:           "tsumugi",
			Name:         "つむぎ",
			VisualCues:   []string{"orange hair"},
			ReferenceURL: "gs://bucket/tsumugi-16x9.png",
			ReferenceURLs: map[string]string{
				"9:16": "gs://bucket/tsumugi-9x16.png",
				"1:1":  "gs://bucket/tsumugi-1x1.png",
			},
			IsDefault: true,
		},
	})

	mc, _ := NewComposer(assetMgr, backend, cm)

	err := mc.PrepareCharacterResources(ctx, []ports.Cut{{CharacterID: "tsumugi"}})
	if err != nil {
		t.Fatalf("PrepareCharacterResources failed: %v", err)
	}

	for _, url := range []string{
		"gs://bucket/tsumugi-16x9.png",
		"gs://bucket/tsumugi-9x16.png",
		"gs://bucket/tsumugi-1x1.png",
	} {
		if uri := mc.GetResourceURI(url); uri == "" {
			t.Errorf("resource for %q not cached", url)
		}
	}

	if assetMgr.uploadCount != 3 {
		t.Errorf("Expected 3 uploads (one per aspect ratio variant), got %d", assetMgr.uploadCount)
	}
}

func TestNewComposer_RequiresCharacters(t *testing.T) {
	_, err := NewComposer(&mockAssetManager{}, &mockBackend{}, nil)
	if err == nil {
		t.Fatal("expected error for nil characters")
	}
}
