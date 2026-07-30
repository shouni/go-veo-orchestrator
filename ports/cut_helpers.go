package ports

import (
	"sort"
	"strings"

	characterkit "github.com/shouni/go-character-kit/character"
)

// UniqueCharacterIDs はカットのスライスから重複しない CharacterID を抽出します。
func (cs Cuts) UniqueCharacterIDs() []string {
	set := make(map[string]struct{})
	for _, cut := range cs {
		if cut.CharacterID != "" {
			set[cut.CharacterID] = struct{}{}
		}
	}

	uniqueIDs := make([]string, 0, len(set))
	for id := range set {
		uniqueIDs = append(uniqueIDs, id)
	}
	sort.Strings(uniqueIDs)

	return uniqueIDs
}

// EffectiveDurationSec は、このカットの実際の尺（秒）を返します。DurationSec が未設定
// （0以下）のレシピでは StartSec / EndSec から導出します。手書き JSON や部分的にしか
// 正規化されていないレシピを扱う箇所が、毎回同じフォールバックを書かずに済むように
// しています。どちらからも決まらない場合は 0 を返します。
func (c Cut) EffectiveDurationSec() float64 {
	if c.DurationSec > 0 {
		return c.DurationSec
	}
	if c.EndSec > c.StartSec {
		return c.EndSec - c.StartSec
	}
	return 0
}

// ResetGeneration は、このカットを再生成させるために生成結果の状態を消します。
// Status は pending に戻り、VideoID / VideoURL / IsChainStart がクリアされます。
// keepKeyframe が false の場合はさらに KeyframeReference と IsSectionStart もクリアし、
// このカットに新しいキーフレームを作り直す前提にします。true の場合は、保存済みの
// キーフレーム参照を再利用する呼び出し（既存ジョブの一部カットだけ動画を作り直す等）の
// ためにどちらも保持します。
func (c *Cut) ResetGeneration(keepKeyframe bool) {
	c.Status = CutStatusPending
	c.VideoID = ""
	c.VideoURL = ""
	c.IsChainStart = false
	if !keepKeyframe {
		c.KeyframeReference = ""
		c.IsSectionStart = false
	}
}

// CutReferenceImages は、このカットの referenceImages（Veo の reference_to_video、
// asset タイプ・最大3枚）用 URI リストを組み立てます。並びは [キャラクター立ち絵,
// キーフレーム] で、空白のみの参照は除外します。参照が1つもなければ nil を返し、
// adapter 側はキーフレームの image 入力（image_to_video）へフォールバックします。
//
// この関数が referenceImages の唯一の組み立て規則です。動画生成リクエストの構築
// （DefaultVideoRequestBuilder）と、そのリクエストの生成モード判定
// （ClassifyVeoRequest 経由の尺の正規化・プロンプト選択）が同じリストを見るため、
// 「尺は reference_to_video 前提で8秒に丸めたのに、実際のリクエストは image_to_video
// だった」というズレが構造的に起こりません。
func CutReferenceImages(cut Cut, characters *characterkit.Characters) []string {
	var refs []string
	if characters != nil {
		if char := characters.GetCharacter(strings.TrimSpace(cut.CharacterID)); char != nil {
			if ref := strings.TrimSpace(char.ReferenceURL); ref != "" {
				refs = append(refs, ref)
			}
		}
	}
	if ref := strings.TrimSpace(cut.KeyframeReference); ref != "" {
		refs = append(refs, ref)
	}
	return refs
}

// NextLastFrameReference は、cs[i] の終了フレーム（frames_to_video 補間の lastFrame）と
// して使う「次カットのキーフレーム参照」を返します。次カットの開始フレームでこのカットを
// 終えることで、カット境界の繋ぎ（構図・キャラの見た目）を滑らかにします。
// 以下の場合は空文字を返し、終了フレーム指定なしで生成させます:
//
//   - 次カットが無い、または次カットにキーフレームが無い
//   - 次カットがセクション境界（IsSectionStart）。意図的な場面転換なので、現カットの絵を
//     次セクションの構図へ寄せない
//   - キャラクターが異なる（補間中にキャラ同士がモーフィングしてしまう）
//   - 現カットと同一のキーフレーム（尺分割で同じキーフレームを共有するカット等。
//     終了フレーム = 開始フレームを強制すると動きが殺される）
func (cs Cuts) NextLastFrameReference(i int) string {
	if i < 0 || i+1 >= len(cs) {
		return ""
	}
	cur, next := cs[i], cs[i+1]
	ref := strings.TrimSpace(next.KeyframeReference)
	if ref == "" || next.IsSectionStart {
		return ""
	}
	if !strings.EqualFold(strings.TrimSpace(cur.CharacterID), strings.TrimSpace(next.CharacterID)) {
		return ""
	}
	if ref == strings.TrimSpace(cur.KeyframeReference) {
		return ""
	}
	return ref
}
