package ports

import "strings"

// VeoGenerationMode は、1つの動画生成リクエストが Veo のどの生成機能で解釈されるかを
// 表します。
//
// このモードは1つのリクエストにつき一度だけ決まり、以下の判断がすべてこの1つの値を
// 共有します:
//
//   - adapter（呼び出し側の ports.VideoRunner 実装）が組み立てる Veo リクエスト本文
//   - そのリクエストに許される尺（DurationsForMode。reference_to_video は8秒固定、
//     video_extension は7秒固定、それ以外は {4,6,8}）
//   - 生成モードごとに前提の異なるプロンプト（開始フレームあり／参照画像あり／
//     前クリップの継続）の選択
//
// 判定を1箇所（ClassifyVeoRequest）に閉じているのは、これらがズレると「参照画像に
// 合わせろ」と指示しながら参照画像を送らない、といった無意味なリクエストになるためです。
type VeoGenerationMode string

const (
	// VeoModeImageToVideo はキーフレーム画像を image 入力とする image_to_video です。
	// 画像参照が一切ないリクエスト（テキストのみ）もここへ倒します。
	VeoModeImageToVideo VeoGenerationMode = "image_to_video"
	// VeoModeFramesToVideo は開始フレームを image 入力、終了フレームを lastFrame 入力と
	// する first/last frame 補間です（Veo 2 / Veo 3.1 系のみ、Fast も対応）。
	VeoModeFramesToVideo VeoGenerationMode = "frames_to_video"
	// VeoModeReferenceToVideo は [キャラ立ち絵, キーフレーム] を referenceImages とする
	// reference_to_video です（Veo 3 系の非 Fast モデルのみ、8秒固定）。
	VeoModeReferenceToVideo VeoGenerationMode = "reference_to_video"
	// VeoModeVideoExtension は前カット動画を video 入力とする video_extension
	// （video-to-video 継続）です。このモードでは画像参照は一切送られません。
	VeoModeVideoExtension VeoGenerationMode = "video_extension"
)

// VeoCapabilities は、実際に使われる VideoRunner／モデルが Veo のオプション機能に
// 対応しているかを表します。ClassifyVeoRequest の入力で、adapter はモデル名から、
// 呼び出し側は RunnerCapabilities（Runner のオプションインターフェース）から導出します。
type VeoCapabilities struct {
	// ReferenceImages は referenceImages（reference_to_video、8秒固定）への対応です。
	ReferenceImages bool
	// LastFrame は lastFrame（first/last frame 補間）への対応です。
	LastFrame bool
}

// ReferenceImagesSupporter は、reference_to_video（referenceImages、Veo 3系の非Fastモデル
// のみ対応で8秒固定）を使えるかを問い合わせられる VideoRunner のオプションインター
// フェースです。カットの尺を Veo のサポート値へ正規化する処理が、モデル判定ロジックを
// 重複実装せずに「このカットは8秒固定になるか」を判断するために使います。
type ReferenceImagesSupporter interface {
	SupportsReferenceImages() bool
}

// LastFrameSupporter は、frames_to_video（image + lastFrame の first/last frame 補間、
// Veo 2 / Veo 3.1 系のみ対応）を使えるかを問い合わせられる VideoRunner のオプション
// インターフェースです。
type LastFrameSupporter interface {
	SupportsLastFrame() bool
}

// RunnerCapabilities は VideoRunner のオプションインターフェース
// （ReferenceImagesSupporter / LastFrameSupporter）から VeoCapabilities を導出します。
// インターフェースを実装しない Runner（テストダブル等）は各機能とも false になり、
// image_to_video 側へ倒れます。
func RunnerCapabilities(runner VideoRunner) VeoCapabilities {
	caps := VeoCapabilities{}
	if rs, ok := runner.(ReferenceImagesSupporter); ok {
		caps.ReferenceImages = rs.SupportsReferenceImages()
	}
	if ls, ok := runner.(LastFrameSupporter); ok {
		caps.LastFrame = ls.SupportsLastFrame()
	}
	return caps
}

// ClassifyVeoRequest は、このリクエストが Veo のどの生成機能で解釈されるかを判定します。
// adapter のリクエスト本文構築と、呼び出し側のプロンプト・尺選択が同じ判定を共有する
// ための唯一の分岐点で、優先順位は次のとおりです:
//
//  1. video_extension — usePreviousVideo が有効で、PreviousVideoID が gs:// 参照のとき。
//     Veo は video と referenceImages / image を併用できないため、以降の画像参照は
//     すべて無視されます。
//  2. reference_to_video — 参照画像 URI が1つ以上あり、モデルが referenceImages に
//     対応しているとき。
//  3. frames_to_video — 開始フレーム（ImageReference または InputImage）と
//     LastFrameReference が両方あり、モデルが lastFrame に対応しているとき
//     （Veo の lastFrame は image とセットでのみ有効）。
//  4. image_to_video — それ以外すべて。
func ClassifyVeoRequest(req VideoGenerationRequest, usePreviousVideo bool, caps VeoCapabilities) VeoGenerationMode {
	if usePreviousVideo && strings.HasPrefix(strings.TrimSpace(req.PreviousVideoID), "gs://") {
		return VeoModeVideoExtension
	}
	if caps.ReferenceImages && hasAnyReferenceImage(req.ReferenceImages) {
		return VeoModeReferenceToVideo
	}
	if caps.LastFrame && hasStartImage(req) && strings.TrimSpace(req.LastFrameReference) != "" {
		return VeoModeFramesToVideo
	}
	return VeoModeImageToVideo
}

// hasAnyReferenceImage は空白のみのエントリを除いた参照画像が1つ以上あるかを返します
// （adapter が空白 URI を除外して参照なしとして扱う規則と対）。
func hasAnyReferenceImage(refs []string) bool {
	for _, ref := range refs {
		if strings.TrimSpace(ref) != "" {
			return true
		}
	}
	return false
}

// hasStartImage は開始フレームとなる画像入力（GCS 参照またはインラインバイト列）が
// あるかを返します。
func hasStartImage(req VideoGenerationRequest) bool {
	return strings.TrimSpace(req.ImageReference) != "" || len(req.InputImage) > 0
}
