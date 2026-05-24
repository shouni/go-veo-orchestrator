package layout

import (
	"testing"

	"github.com/shouni/go-veo-orchestrator/ports"
)

func TestPageResourceCollector(t *testing.T) {
	// 共通のセットアップ
	assetMgr := &mockAssetManager{}
	cm := ports.CharactersMap{
		"zundamon": {ID: "zundamon", Name: "ずんだもん", ReferenceURL: "gs://bucket/zunda.png"},
		"metan":    {ID: "metan", Name: "めたん", ReferenceURL: "https://example.com/metan.png"},
	}

	t.Run("Standard Asset Collection", func(t *testing.T) {
		backend := &mockBackend{isVertex: false} // AI Studio モード (File API必須)
		composer, _ := NewMangaComposer(assetMgr, backend, cm)

		// 偽のキャッシュをセット（本来は PrepareCharacterResources / PreparePanelResources で入るもの）
		// 内部フィールドへのアクセスは Mutex で保護する
		composer.mu.Lock()
		composer.resourceMap.character["zundamon"] = "https://file-api/zunda"
		composer.resourceMap.panel["gs://bucket/panel1.png"] = "https://file-api/p1"
		composer.mu.Unlock()

		collector := newPageResourceCollector(composer)

		panels := []ports.Panel{
			{SpeakerID: "zundamon", ReferenceURL: "gs://bucket/panel1.png"},
			{SpeakerID: "zundamon", ReferenceURL: "gs://bucket/panel1.png"}, // 重複パネル
		}

		collector.addCharacterAssets(panels)
		collector.addPanelAssets(panels)

		rm := collector.resourceMap

		// 1. 重複が排除されているか (zundamon 立ち絵 と panel1 参照画像 の2つだけのはず)
		if len(rm.OrderedAssets) != 2 {
			t.Errorf("Expected 2 ordered assets, got %d", len(rm.OrderedAssets))
		}

		// 2. マッピングが正しいか
		// キャラクターIDをキーに OrderedAssets のインデックスが引けるか
		if idx, ok := rm.CharacterFiles["zundamon"]; !ok {
			t.Error("Character mapping missing in ResourceMap")
		} else if rm.OrderedAssets[idx].FileAPIURI != "https://file-api/zunda" {
			t.Errorf("Unexpected FileAPIURI for character: %s", rm.OrderedAssets[idx].FileAPIURI)
		}

		// パネル参照URLをキーに OrderedAssets のインデックスが引けるか
		if idx, ok := rm.PanelFiles["gs://bucket/panel1.png"]; !ok {
			t.Error("Panel mapping missing in ResourceMap")
		} else if idx != 1 {
			t.Errorf("Panel mapping failed, expected index 1, got %d", idx)
		}
	})

	t.Run("Vertex AI Mode Bypass", func(t *testing.T) {
		backend := &mockBackend{isVertex: true} // Vertex AI モード (GCS直参照OK)
		composer, _ := NewMangaComposer(assetMgr, backend, cm)

		// File API URI が空（アップロードしていない）状態をシミュレート
		collector := newPageResourceCollector(composer)

		panels := []ports.Panel{
			{SpeakerID: "zundamon", ReferenceURL: "gs://bucket/zunda.png"},
		}

		// Vertex AI モードなら FileAPIURI が空でも gs:// であれば登録を継続する
		collector.addCharacterAssets(panels)

		if len(collector.resourceMap.OrderedAssets) == 0 {
			t.Error("Asset should be registered in Vertex AI mode even if FileAPIURI is empty (GCS Bypass)")
		}

		asset := collector.resourceMap.OrderedAssets[0]
		if asset.ReferenceURL != "gs://bucket/zunda.png" {
			t.Errorf("Unexpected ReferenceURL: %s", asset.ReferenceURL)
		}
	})

	t.Run("Invalid/Missing Assets", func(t *testing.T) {
		backend := &mockBackend{isVertex: false}
		composer, _ := NewMangaComposer(assetMgr, backend, cm)
		collector := newPageResourceCollector(composer)

		panels := []ports.Panel{
			{SpeakerID: "non-existent"}, // マップにないキャラクター
			{SpeakerID: "zundamon"},     // キャッシュにない(fileURI空) & Vertexモードでない
		}

		collector.addCharacterAssets(panels)

		if len(collector.resourceMap.OrderedAssets) != 0 {
			t.Error("Should not register invalid or missing assets when FileAPIURI is empty in standard mode")
		}
	})

	t.Run("Sorted Panel Assets", func(t *testing.T) {
		backend := &mockBackend{isVertex: true}
		composer, _ := NewMangaComposer(assetMgr, backend, cm)
		collector := newPageResourceCollector(composer)

		panels := []ports.Panel{
			{ReferenceURL: "gs://bucket/b.png"},
			{ReferenceURL: "gs://bucket/a.png"},
		}

		// 内部的に ReferenceURL の昇順でソートされて OrderedAssets に追加されるか確認
		collector.addPanelAssets(panels)

		if len(collector.resourceMap.OrderedAssets) < 2 {
			t.Fatalf("Expected 2 assets, got %d", len(collector.resourceMap.OrderedAssets))
		}

		if collector.resourceMap.OrderedAssets[0].ReferenceURL != "gs://bucket/a.png" {
			t.Errorf("Assets should be sorted by ReferenceURL, got %s first, want gs://bucket/a.png",
				collector.resourceMap.OrderedAssets[0].ReferenceURL)
		}
	})
}
