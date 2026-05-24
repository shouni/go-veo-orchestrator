package ports

import imagePorts "github.com/shouni/gemini-image-kit/ports"

// MangaResponse は AI モデルから返される台本全体の構造です。
type MangaResponse struct {
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Panels      []Panel `json:"panels"`
}

// Panel は漫画の1ページまたは1パネルの構成、セリフ、話者情報を保持します。
type Panel struct {
	Page         int    `json:"page"`
	VisualAnchor string `json:"visual_anchor"`
	Dialogue     string `json:"dialogue"`
	SpeakerID    string `json:"speaker_id"`
	ReferenceURL string `json:"reference_url"`
}

// Panels は Panel のスライスに対するカスタム型です。
type Panels []Panel

// Page は物理的な1枚の画像（複数のパネルを統合したもの）を表します。
// 複数のパネルを1枚の画像に合成する場合に活用します。
type Page struct {
	PageNumber int
	ImageURL   string
	Panels     []Panel
}

// ResourceMap は、文字やパネルのリソースファイルをインデックスや順序付きの参照にマッピングするための構造体です。
type ResourceMap struct {
	// CharacterFiles は SpeakerID から OrderedAssets のインデックスへのマップです。
	CharacterFiles map[string]int
	// PanelFiles は ReferenceURL から OrderedAssets のインデックスへのマップです。
	PanelFiles map[string]int
	// OrderedAssets は Gemini に渡す画像アセット（File API URI と元の URL のペア）の順序付きリストです。
	OrderedAssets []imagePorts.ImageURI
}
