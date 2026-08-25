// Package video は、動画レシピのドメインモデル（Recipe・Cut とその操作）と、
// 生成リクエスト／レスポンスのデータ型を定義します。このリポジトリで最下層の
// パッケージで、リポジトリ内の何にも依存しません。
package video

import (
	"maps"
	"slices"
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
	return slices.Sorted(maps.Keys(set))
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
// キャラクターの立ち絵は、このカットのアスペクト比（Cut.AspectRatio、Recipe.Normalize が
// Recipe.AspectRatio から伝播）に一致するもの（Character.ReferenceURLs）があればそちらを
// 使います。keyframe.Generator が ReferenceURLFor で同じ選び方をしているのに、ここだけ
// 比率を無視した ReferenceURL 固定だと、同じリクエストの中で「比率の合ったキーフレーム」と
// 「比率の違う立ち絵」が並び、Veo が参照から拾う色や小物の位置がブレます。
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
			if ref := strings.TrimSpace(char.ReferenceURLFor(cut.AspectRatio)); ref != "" {
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

// FillAudioReference は、AudioReference が未設定のカットへ audioURL を補います。
// タスクで与えられた音源をレシピの全カットに紐づける用途で、カット側に既に
// 明示された参照は上書きしません。空の audioURL は何もしません。
func (cs Cuts) FillAudioReference(audioURL string) {
	audioURL = strings.TrimSpace(audioURL)
	if audioURL == "" {
		return
	}
	for i := range cs {
		if strings.TrimSpace(cs[i].AudioReference) == "" {
			cs[i].AudioReference = audioURL
		}
	}
}

// FillCharacterID は、CharacterID が未設定のカットへ characterID を補います。
// カット側に既に明示されたキャラクターは上書きしません。空の characterID は
// 何もしません。
func (cs Cuts) FillCharacterID(characterID string) {
	characterID = strings.TrimSpace(characterID)
	if characterID == "" {
		return
	}
	for i := range cs {
		if strings.TrimSpace(cs[i].CharacterID) == "" {
			cs[i].CharacterID = characterID
		}
	}
}

// IndexOf は、cut_index（レシピ上の1始まりの番号）を持つカットの位置（0始まり）を
// 返します。見つからない場合は -1 です。
func (cs Cuts) IndexOf(cutIndex int) int {
	for i := range cs {
		if cs[i].CutIndex == cutIndex {
			return i
		}
	}
	return -1
}
