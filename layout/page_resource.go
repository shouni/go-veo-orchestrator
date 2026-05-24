package layout

import (
	"sort"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	"github.com/shouni/go-veo-orchestrator/ports"
)

type pageResourceCollector struct {
	composer    *MangaComposer
	resourceMap *ports.ResourceMap
	addedByURL  map[string]int
	isVertex    bool
}

// newPageResourceCollector は指定されたコンポーザーからページリソースコレクターを初期化します。
func newPageResourceCollector(composer *MangaComposer) *pageResourceCollector {
	return &pageResourceCollector{
		composer: composer,
		isVertex: composer.BackendProvider.IsVertexAI(),
		resourceMap: &ports.ResourceMap{
			CharacterFiles: make(map[string]int),
			PanelFiles:     make(map[string]int),
		},
		addedByURL: make(map[string]int),
	}
}

// addCharacterAssets は指定されたパネルのキャラクターアセットをリソースマップに追加します。
// Vertex AI モード時は GCS パス (gs://) を優先し、File API URI が空でも登録を継続します。
func (c *pageResourceCollector) addCharacterAssets(panels []ports.Panel) {
	for _, speakerID := range ports.Panels(panels).UniqueSpeakerIDs() {
		char := c.composer.CharactersMap.GetCharacter(speakerID)
		if char == nil || char.ReferenceURL == "" {
			continue
		}

		fileURI := c.composer.GetCharacterResourceURI(char.ID)
		if !c.canRegister(fileURI, char.ReferenceURL) {
			continue
		}

		idx := c.addAsset(imagePorts.ImageURI{
			ReferenceURL: char.ReferenceURL,
			FileAPIURI:   fileURI,
		})
		c.resourceMap.CharacterFiles[speakerID] = idx
	}
}

// addPanelAssets はソートされたパネルアセットをリソースマップに追加します。
func (c *pageResourceCollector) addPanelAssets(panels []ports.Panel) {
	panelAssets := c.sortedPanelAssets(panels)
	for _, asset := range panelAssets {
		idx := c.addAsset(asset)
		c.resourceMap.PanelFiles[asset.ReferenceURL] = idx
	}

	for _, panel := range panels {
		if panel.ReferenceURL == "" {
			continue
		}
		if idx, ok := c.addedByURL[panel.ReferenceURL]; ok {
			c.resourceMap.PanelFiles[panel.ReferenceURL] = idx
		}
	}
}

// sortedPanelAssets は指定されたパネルのリソースをソートして返します。
func (c *pageResourceCollector) sortedPanelAssets(panels []ports.Panel) []imagePorts.ImageURI {
	var panelAssets []imagePorts.ImageURI

	for _, panel := range panels {
		if panel.ReferenceURL == "" {
			continue
		}
		if _, exists := c.addedByURL[panel.ReferenceURL]; exists {
			continue
		}

		fileURI := c.composer.GetPanelResourceURI(panel.ReferenceURL)
		if !c.canRegister(fileURI, panel.ReferenceURL) {
			continue
		}

		panelAssets = append(panelAssets, imagePorts.ImageURI{
			ReferenceURL: panel.ReferenceURL,
			FileAPIURI:   fileURI,
		})
		// 重複追加防止のための一時マーク
		c.addedByURL[panel.ReferenceURL] = -1
	}

	sort.Slice(panelAssets, func(i, j int) bool {
		return panelAssets[i].ReferenceURL < panelAssets[j].ReferenceURL
	})

	return panelAssets
}

// addAsset は指定されたアセットを追加し、そのインデックスを返します。
func (c *pageResourceCollector) addAsset(asset imagePorts.ImageURI) int {
	if idx, exists := c.addedByURL[asset.ReferenceURL]; exists && idx >= 0 {
		return idx
	}

	idx := len(c.resourceMap.OrderedAssets)
	c.resourceMap.OrderedAssets = append(c.resourceMap.OrderedAssets, asset)
	c.addedByURL[asset.ReferenceURL] = idx
	return idx
}

// canRegister は、リソースを登録可能かどうかを判定します。
// Vertex AI モードで GCS パスを持つか、あるいは File API URI が存在する場合に true を返します。
func (c *pageResourceCollector) canRegister(fileURI, referenceURL string) bool {
	// File API URI が既にあるなら OK
	if fileURI != "" {
		return true
	}
	// Vertex AI モード かつ GCS URI なら OK (File API URI が空でも許容)
	return c.isVertex && IsGCSURI(referenceURL)
}
