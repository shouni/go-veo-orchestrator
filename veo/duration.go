// Package veo は、Veo API の制約（生成モードの判定・モードごとに許される尺・
// カット尺の計画）を扱います。動画生成そのものは行わず、リクエストがどのモードへ
// 解釈されるかと、そのモードで許される尺だけを決めます。実際の Veo 呼び出しは
// 呼び出し側が注入する ports.VideoRunner の仕事です。
package veo

import (
	"math"
	"slices"
)

// Veo が受け付けるカット尺（秒）に関する定数です。Veo は任意長の動画を生成できず、
// 生成モードごとに離散的な尺しか受け付けないため、カット尺の計画・正規化・検証は
// すべてこの値を基準に行います。
const (
	// MaxCutDurationSec は image_to_video が受け付ける最大カット尺（秒）です。
	MaxCutDurationSec = 8.0

	// VideoExtensionDurationSec は video_extension（video-to-video、前カット動画を
	// PreviousVideoID として引き継ぐ生成）が受け付ける唯一のカット尺（秒）です。
	VideoExtensionDurationSec = 7.0

	// ContinuationMaxDurationSec は、1本の継続チェーンの累積尺（秒）がこの値に達する
	// 手前で新しいチェーンへリセットするための閾値です。
	//
	// video_extension が「前の動画」として受け付けられる累積尺の API 上の上限は30秒です
	// （累積29秒までは成功し、累積36秒を渡すと "Video duration 36 seconds exceeds the
	// maximum duration 30 seconds" (code=3) で失敗する）。ただし video_extension は前回の
	// 生成結果を条件入力として再利用する性質上、継続を重ねるたびに彩度・コントラストが
	// ドリフトして蓄積します（実ジョブで確認: 継続1回目で彩度+20%、以降のラウンドでも
	// コントラストが単調に増加し続けた）。そのため API 上限の30秒ではなく、より低い
	// この値で早めにリセットし、1チェーンあたりの継続回数＝ドリフトの蓄積量を抑えます。
	ContinuationMaxDurationSec = 24.0
)

// imageToVideoDurations などは、各生成モードで Veo が受け付ける尺（秒）の昇順リストです。
// 呼び出し側が誤って書き換えないよう、公開しているのはコピーを返す関数だけです。
var (
	imageToVideoDurations     = []float64{4, 6, 8}
	referenceToVideoDurations = []float64{8}
	videoExtensionDurations   = []float64{VideoExtensionDurationSec}
)

// ImageToVideoDurationsSec は image_to_video / frames_to_video が受け付けるカット尺（秒）を
// 昇順で返します。
func ImageToVideoDurationsSec() []float64 { return slices.Clone(imageToVideoDurations) }

// ReferenceToVideoDurationsSec は reference_to_video（referenceImages）が受け付けるカット尺
// （秒）を返します。image_to_video の {4,6,8} とは異なり8秒固定です。
func ReferenceToVideoDurationsSec() []float64 {
	return slices.Clone(referenceToVideoDurations)
}

// DurationsForMode は、指定された生成モードで Veo が受け付けるカット尺（秒）を昇順で
// 返します。frames_to_video は image 入力を伴うため image_to_video と同じ集合です。
func DurationsForMode(mode GenerationMode) []float64 {
	switch mode {
	case ModeReferenceToVideo:
		return ReferenceToVideoDurationsSec()
	case ModeVideoExtension:
		return slices.Clone(videoExtensionDurations)
	case ModeImageToVideo, ModeFramesToVideo:
		return ImageToVideoDurationsSec()
	default:
		return ImageToVideoDurationsSec()
	}
}

// IsSupportedDuration は、durationSec が mode で Veo に受け付けられる尺かを返します。
// 尺は整数秒の離散値なので、浮動小数の丸め誤差を許容して比較します。
func IsSupportedDuration(durationSec float64, mode GenerationMode) bool {
	for _, candidate := range DurationsForMode(mode) {
		if math.Abs(durationSec-candidate) < durationEpsilon {
			return true
		}
	}
	return false
}

// durationEpsilon は尺の比較に使う許容誤差です。尺は整数秒しか取らないため、
// 誤差拡散などで生じる微小なズレを同値として扱えるだけの幅があれば足ります。
const durationEpsilon = 1e-6

// SnapDuration は尺を candidates のうち最も近いものへ丸めます（同距離なら長い方）。
// candidates が空の場合は durationSec をそのまま返します。
func SnapDuration(durationSec float64, candidates []float64) float64 {
	if len(candidates) == 0 {
		return durationSec
	}
	best := candidates[0]
	for _, candidate := range candidates {
		if math.Abs(durationSec-candidate) <= math.Abs(durationSec-best) {
			best = candidate
		}
	}
	return best
}

// ChainDurations は、1本の継続チェーン（ベース1カット + video_extension の継続カット列）
// として生成できる合計尺（秒）の候補を昇順で返します。bases はチェーン先頭カットに
// 許される尺（image_to_video なら {4,6,8}、reference_to_video なら {8}）です。
//
// 各候補は「ベース尺 + VideoExtensionDurationSec × 継続回数」で、累積尺が
// ContinuationMaxDurationSec を超える手前まで伸ばした値です。既定の
// image_to_video ベースでは {4,6,8,11,13,15,18,20,22} になります。
// チェーン単位で尺を計画する呼び出し側が、実現可能な合計尺の候補として使います。
func ChainDurations(bases []float64) []float64 {
	seen := make(map[float64]bool, len(bases))
	out := make([]float64, 0, len(bases))
	for _, base := range bases {
		for d := base; ; d += VideoExtensionDurationSec {
			if !seen[d] {
				seen[d] = true
				out = append(out, d)
			}
			if d+VideoExtensionDurationSec > ContinuationMaxDurationSec {
				break
			}
		}
	}
	slices.Sort(out)
	return out
}
