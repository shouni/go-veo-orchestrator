package veo

import (
	"math"
	"strings"

	characterkit "github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-veo-orchestrator/video"
)

// このファイルはカット尺のプランナーです。レシピ上の任意尺のカット列を、Veo が実際に
// 受け付ける離散尺（duration.go）のカット列へ正規化する純関数群を提供します。
//
// 尺のルールと、それを使う計画の両方をこのパッケージに置いています。プランナーだけを
// 消費者に書かせると、ルールと計画が別リポジトリに割れてドリフトするためです
// （実際にそうなっていました）。

// ExpandCutsToSupportedDurations は各カットの尺を Veo のサポート値へ正規化します。
// 8 秒を超えるカットは同じキーフレーム・プロンプトを引き継いだサブカット列へ分割し、
// 歌詞（Dialogue）は行単位でサブカットへ均等配分します。分割後は CutIndex を 1 から振り直します。
//
// usePreviousVideo が true の場合、先頭カット以降（PreviousVideoURI を伴い video_extension で
// 生成される想定のカット）は 7 秒固定へ揃えます。ただし累積尺が ContinuationMaxDurationSec に
// 達する手前でチェーンをリセットし、リセットされたカットは PreviousVideoURI を使わない新規
// ベース（image_to_video、{4,6,8}秒）として扱います（実行側の lastVideoID リセットと対）。
// 生成済みカットは実動画の尺と metadata がずれないよう変更しませんが、再開時にチェーン状態を
// 正しく引き継ぐため累積尺の計算には含めます。
//
// 曲のセクションが変わる境目では、累積尺に余裕があってもチェーンをリセットします。所属
// セクションは cut.SectionIndex（VideoRecipe.Normalize が StartSec から補完）で判定します。
// 技術的リセットとの違いは IsSectionStart で示し、実行側はこのフラグが立つカットで直前
// チェーンの最終フレーム引き継ぎをスキップします（意図した場面転換の直前で、次セクションの
// 構図へ寄せないため）。
//
// characters / referenceImagesSupported はカットごとの許容尺の判定に使います
// （AllowedCutDurations 参照）。事前計画されたチェーンブロック（IsChainStart 付きの未生成
// カット）の扱いは splitChainCutIntoSupportedDurations を参照してください。IsChainStart を
// 持たない未生成カット（旧レシピなど）は、貪欲な分割と累積尺ベースのチェーン形成で処理します。
func ExpandCutsToSupportedDurations(cuts []video.Cut, usePreviousVideo bool, characters *characterkit.Characters, referenceImagesSupported bool) []video.Cut {
	expanded := make([]video.Cut, 0, len(cuts))
	for _, cut := range cuts {
		var subCuts []video.Cut
		if usePreviousVideo && cut.IsChainStart && !cut.IsGenerated() {
			subCuts = splitChainCutIntoSupportedDurations(cut, AllowedCutDurations(cut, characters, referenceImagesSupported))
		} else {
			subCuts = SplitCutBySupportedDurations(cut, AllowedCutDurations(cut, characters, referenceImagesSupported))
		}
		expanded = append(expanded, subCuts...)
	}
	cumulative := 0.0
	for i := range expanded {
		expanded[i].CutIndex = i + 1
		if !usePreviousVideo {
			continue
		}
		if expanded[i].IsGenerated() {
			// 生成済みカットがそれ自体チェーンの起点（IsChainStart、先頭カットまたは
			// 過去のリセット）だった場合、累積尺はそのカット自身の尺から数え直す。
			// 常に加算するだけだと、一度リセットが起きた後もそれ以前のチェーンの
			// 累積尺を引きずり続けてしまい、以降のカットが（実際には累積尺に余裕が
			// あるのに）毎回誤ってリセット扱いになる（1カットずつ再開する実行側の
			// 性質上、この関数は毎回全カットを再計算する）。
			if i == 0 || expanded[i].IsChainStart {
				cumulative = expanded[i].DurationSec
			} else {
				cumulative += expanded[i].DurationSec
			}
			continue
		}
		// IsSectionStart（cut.ChainControl）は2つの異なる理由で立つ、意味が重なった
		// フラグです。ここではその2つを別々に判定してから合成し、どちらの理由で
		// リセットになったかをコードとして追跡できるようにしています。
		//
		//   isSceneReset: シーン分割側がこの関数より前に立てた値です。1つの音楽
		//   セクション内で複数シーンに分割された際の「シーン内リセット」で、
		//   SectionIndex は前後で変わりません（SectionIndex 比較では検出できない）。
		//
		//   isRealSectionBoundary: cut.SectionIndex（1始まり、0は未割り当て）が
		//   前カットと異なる、実際の曲のセクション境界です。分割前カットの
		//   SectionIndex はサブカットへコピーされるため（buildSubCutsFromDurations の
		//   sub := cut）、分割自体はセクション境界とは見なされません。
		//
		// 両者とも「直前チェーンの最終フレームを引き継がない」という同じ下流の
		// 挙動を必要とするため、最終的には同じ IsSectionStart へ合成して書き戻します。
		isSceneReset := expanded[i].IsSectionStart
		isRealSectionBoundary := i > 0 && expanded[i].SectionIndex != 0 && expanded[i].SectionIndex != expanded[i-1].SectionIndex
		isSectionStart := isSceneReset || isRealSectionBoundary
		if isSectionStart {
			cumulative = 0
		}
		if expanded[i].IsChainStart || cumulative == 0 || cumulative+VideoExtensionDurationSec > ContinuationMaxDurationSec {
			// 新規チェーンの先頭（曲頭、セクション境界、リセット直後、または事前計画
			// されたチェーンブロックの起点）。分割時に既に割り当てた尺
			// （image_to_videoなら{4,6,8}秒、reference_to_videoなら8秒固定）をそのまま使う。
			cumulative = expanded[i].DurationSec
			if isSectionStart {
				expanded[i].IsSectionStart = true
			}
			continue
		}
		expanded[i].DurationSec = VideoExtensionDurationSec
		expanded[i].EndSec = expanded[i].StartSec + VideoExtensionDurationSec
		cumulative += VideoExtensionDurationSec
	}
	return expanded
}

// AllowedCutDurations は、指定されたカットが reference_to_video（referenceImages）と
// image_to_video のどちらで生成されるかに応じて、Veo がそのカットに対して受け付ける尺
// （秒）の候補リストを返します（referenceImagesSupported が false の場合、モデルが
// referenceImages に対応していないため image_to_video 用の {4,6,8} を返します）。
func AllowedCutDurations(cut video.Cut, characters *characterkit.Characters, referenceImagesSupported bool) []float64 {
	if CutUsesReferenceImages(cut, characters, referenceImagesSupported) {
		return ReferenceToVideoDurationsSec()
	}
	return ImageToVideoDurationsSec()
}

// CutUsesReferenceImages は、このカットが Veo の referenceImages（reference_to_video）で
// 生成されるかを返します。実際の生成が組むのと同じ参照画像リスト（CutReferenceImages）で
// リクエストを分類する（ClassifyRequest）ため、尺の正規化と実際の生成が使う判定は
// 構造的に一致します。
func CutUsesReferenceImages(cut video.Cut, characters *characterkit.Characters, referenceImagesSupported bool) bool {
	req := video.GenerationRequest{
		ImageReference:  cut.KeyframeReference,
		ReferenceImages: video.CutReferenceImages(cut, characters),
	}
	caps := Capabilities{ReferenceImages: referenceImagesSupported}
	return ClassifyRequest(req, false, caps) == ModeReferenceToVideo
}

// SplitCutBySupportedDurations は1カットをサポート尺のサブカット列へ分割します。
// 生成済みカットは実動画の尺と metadata がずれないよう、そのまま返します。
// allowedDurations は使用する尺の候補リストで、AllowedCutDurations が呼び出し元で
// 事前に決定します（reference_to_videoなら{8}、image_to_videoなら{4,6,8}）。
// CutIndex の振り直しは行いません（シーン分割など、呼び出し側が自前の採番を持つ
// 経路から使うため。全体の正規化には ExpandCutsToSupportedDurations を使ってください）。
func SplitCutBySupportedDurations(cut video.Cut, allowedDurations []float64) []video.Cut {
	if cut.IsGenerated() {
		return []video.Cut{cut}
	}
	duration := cut.EffectiveDurationSec()
	if duration <= MaxCutDurationSec {
		cut.DurationSec = SnapDuration(duration, allowedDurations)
		cut.EndSec = cut.StartSec + cut.DurationSec
		return []video.Cut{cut}
	}

	// 貪欲に8秒上限で分割し、末尾の端数のみサポート尺へ丸める。
	// Note: 端数が切り上げ方向へ丸められた場合、最後のサブカットの EndSec は元の
	// cut.EndSec を超過し、後続カットとタイムライン上でオーバーラップする可能性が
	// ありますが、Veo の個別カット生成としては問題ないため仕様として許容しています。
	var durations []float64
	for remaining := duration; remaining > 0; {
		d := MaxCutDurationSec
		if remaining < MaxCutDurationSec {
			d = SnapDuration(remaining, allowedDurations)
		}
		durations = append(durations, d)
		remaining -= d
	}
	return buildSubCutsFromDurations(cut, durations)
}

// buildSubCutsFromDurations は1つの親カットを durations に対応するサブカット列へ展開します。
// サブ尺リストの計算方法（貪欲な8秒上限 vs [ベース, 7秒, ...]）だけが呼び出し元ごとに異なり、
// そこから先の組み立て — 親フィールドの引き継ぎ、先頭以外のチェーンフラグのクリア、時間の
// 連続配置、歌詞の均等配分 — は共通です。
func buildSubCutsFromDurations(cut video.Cut, durations []float64) []video.Cut {
	subCuts := make([]video.Cut, 0, len(durations))
	offset := 0.0
	for i, d := range durations {
		sub := cut
		if i > 0 {
			sub.IsChainStart = false
			sub.IsSectionStart = false
		}
		sub.StartSec = cut.StartSec + offset
		sub.DurationSec = d
		sub.EndSec = sub.StartSec + d
		subCuts = append(subCuts, sub)
		offset += d
	}
	lines := SplitDialogueLines(cut.Dialogue)
	for i := range subCuts {
		subCuts[i].Dialogue = DistributeLines(lines, i, len(subCuts))
	}
	return subCuts
}

// splitChainCutIntoSupportedDurations は、シーン分割側が事前計画したチェーンブロック
// （IsChainStart 付き、尺は ChainDurations の候補値）を実際の生成カット列
// [ベース, 7秒, 7秒, ...] へ分割します。先頭サブカットがチェーンの起点で、その尺は
// allowedBases から選びます。以降のサブカットは video_extension の7秒固定です。
//
// reference_to_video のカット（ベース8秒固定）に {4,6} ベース前提のブロック尺（11秒など）が
// 計画されていた場合、ベースは allowedBases へ丸められるため実現尺が計画と数秒ずれますが、
// 計画側も同じ CutUsesReferenceImages 判定で候補を選ぶため通常は一致します。
func splitChainCutIntoSupportedDurations(cut video.Cut, allowedBases []float64) []video.Cut {
	duration := cut.EffectiveDurationSec()
	if duration <= MaxCutDurationSec {
		cut.DurationSec = SnapDuration(duration, allowedBases)
		cut.EndSec = cut.StartSec + cut.DurationSec
		return []video.Cut{cut}
	}
	extensions := int(math.Ceil((duration - MaxCutDurationSec) / VideoExtensionDurationSec))
	base := SnapDuration(duration-float64(extensions)*VideoExtensionDurationSec, allowedBases)

	// [ベース, 7秒 × extensions] のサブ尺列を組み立てる。
	durations := make([]float64, 0, extensions+1)
	durations = append(durations, base)
	for range extensions {
		durations = append(durations, VideoExtensionDurationSec)
	}
	return buildSubCutsFromDurations(cut, durations)
}

// CapCutsTotalDuration は合計尺が maxSec を超えないよう、超過するカット以降を切り詰めます。
// 少なくとも先頭の1カットは残します。
func CapCutsTotalDuration(cuts []video.Cut, maxSec float64) []video.Cut {
	total := 0.0
	for i, cut := range cuts {
		if i > 0 && total+cut.DurationSec > maxSec {
			return cuts[:i]
		}
		total += cut.DurationSec
	}
	return cuts
}

// ChainTailEnd は、target のカットを作り直す際に一緒に作り直す必要がある最後のカットの
// 位置を返します。
//
// 継続チェーンでは、対象カットの動画を作り直すと後続カットの PreviousVideoURI が指す
// 動画が古いままになります。そのため次のチェーン起点の手前までをまとめて作り直します。
// usePreviousVideo=false ではカット同士が動画で繋がらないため、対象カット 1 枚で閉じます。
func ChainTailEnd(cuts []video.Cut, target int, usePreviousVideo bool) int {
	if !usePreviousVideo {
		return target
	}
	end := target
	for j := target + 1; j < len(cuts); j++ {
		if isChainBase(cuts[j]) {
			break
		}
		end = j
	}
	return end
}

// isChainBase は、このカットがチェーンの起点（ベース）かを返します。
// 事前計画されたチェーンは IsChainStart を持ちますが、旧レシピは貪欲な分割で
// 7秒以外の尺がベースになるため、どちらの手掛かりも見ます。
func isChainBase(cut video.Cut) bool {
	return cut.IsChainStart || cut.DurationSec != VideoExtensionDurationSec
}

// SplitDialogueLines は歌詞テキストを空行を除いた行スライスへ分解します。
func SplitDialogueLines(dialogue string) []string {
	var lines []string
	for line := range strings.SplitSeq(dialogue, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// DistributeLines は lines を total 個へ按分し、pos 番目の分を改行で繋いで返します。
// 前のバケットほど前半の行を受け取るため、連続するサブカットへ歌詞がそのままの順で流れます。
func DistributeLines(lines []string, pos, total int) string {
	if len(lines) == 0 {
		return ""
	}
	if total <= 1 {
		return strings.Join(lines, "\n")
	}
	n := len(lines)
	start := pos * n / total
	end := (pos + 1) * n / total
	if start >= n || start >= end {
		return ""
	}
	if end > n {
		end = n
	}
	return strings.Join(lines[start:end], "\n")
}
