package veo

import (
	"strings"

	"github.com/shouni/go-veo-orchestrator/ports"
	"github.com/shouni/go-veo-orchestrator/video"
)

// GenerationMode は、1つの動画生成リクエストが Veo のどの生成機能で解釈されるかを
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
// 判定を1箇所（ClassifyRequest）に閉じているのは、これらがズレると「参照画像に
// 合わせろ」と指示しながら参照画像を送らない、といった無意味なリクエストになるためです。
type GenerationMode string

const (
	// ModeImageToVideo はキーフレーム画像を image 入力とする image_to_video です。
	// 画像参照が一切ないリクエスト（テキストのみ）もここへ倒します。
	ModeImageToVideo GenerationMode = "image_to_video"
	// ModeFramesToVideo は開始フレームを image 入力、終了フレームを lastFrame 入力と
	// する first/last frame 補間です（Veo 2 / Veo 3.1 系のみ、Fast も対応）。
	ModeFramesToVideo GenerationMode = "frames_to_video"
	// ModeReferenceToVideo は [キャラ立ち絵, キーフレーム] を referenceImages とする
	// reference_to_video です（Veo 3 系の非 Fast モデルのみ、8秒固定）。
	ModeReferenceToVideo GenerationMode = "reference_to_video"
	// ModeVideoExtension は前カット動画を video 入力とする video_extension
	// （video-to-video 継続）です。このモードでは画像参照は一切送られません。
	ModeVideoExtension GenerationMode = "video_extension"
)

// Capabilities は、実際に使われる ports.VideoRunner／モデルが Veo のオプション機能に
// 対応しているかを表します。ClassifyRequest の入力で、adapter はモデル名から、
// 呼び出し側は RunnerCapabilities（Runner のオプションインターフェース）から導出します。
type Capabilities struct {
	// ReferenceImages は referenceImages（reference_to_video、8秒固定）への対応です。
	ReferenceImages bool
	// LastFrame は lastFrame（first/last frame 補間）への対応です。
	LastFrame bool
}

// RunnerCapabilities は ports.VideoRunner のオプションインターフェース
// （ReferenceImagesSupporter / LastFrameSupporter）から Capabilities を導出します。
// インターフェースを実装しない Runner（テストダブル等）は各機能とも false になり、
// image_to_video 側へ倒れます。
func RunnerCapabilities(runner ports.VideoRunner) Capabilities {
	caps := Capabilities{}
	if rs, ok := runner.(ports.ReferenceImagesSupporter); ok {
		caps.ReferenceImages = rs.SupportsReferenceImages()
	}
	if ls, ok := runner.(ports.LastFrameSupporter); ok {
		caps.LastFrame = ls.SupportsLastFrame()
	}
	return caps
}

// ClassifyRequest は、このリクエストが Veo のどの生成機能で解釈されるかを判定します。
// adapter のリクエスト本文構築と、呼び出し側のプロンプト・尺選択が同じ判定を共有する
// ための唯一の分岐点で、優先順位は次のとおりです:
//
//  1. video_extension — usePreviousVideo が有効で、PreviousVideoURI が gs:// 参照のとき。
//     Veo は video と referenceImages / image を併用できないため、以降の画像参照は
//     すべて無視されます。
//  2. reference_to_video — 参照画像 URI が1つ以上あり、モデルが referenceImages に
//     対応しているとき。
//  3. frames_to_video — 開始フレーム（ImageReference または InputImage）と
//     LastFrameReference が両方あり、モデルが lastFrame に対応しているとき
//     （Veo の lastFrame は image とセットでのみ有効）。
//  4. image_to_video — それ以外すべて。
func ClassifyRequest(req video.GenerationRequest, usePreviousVideo bool, caps Capabilities) GenerationMode {
	if usePreviousVideo && strings.HasPrefix(strings.TrimSpace(req.PreviousVideoURI), "gs://") {
		return ModeVideoExtension
	}
	if caps.ReferenceImages && hasAnyReferenceImage(req.ReferenceImages) {
		return ModeReferenceToVideo
	}
	if caps.LastFrame && hasStartImage(req) && strings.TrimSpace(req.LastFrameReference) != "" {
		return ModeFramesToVideo
	}
	return ModeImageToVideo
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
func hasStartImage(req video.GenerationRequest) bool {
	return strings.TrimSpace(req.ImageReference) != "" || len(req.InputImage) > 0
}

// ModelCapabilities は、Veo のモデル名からオプション機能への対応を導出します。
// モデル名→対応機能の規則はこの関数が唯一の定義元です（以前は adapter 側が文字列
// 前方一致で再導出しており、「ルールはライブラリが持つ」という原則が破れていました）。
//
//   - referenceImages（reference_to_video、8秒固定）: Veo 3 系のみ。Fast は非対応。
//   - lastFrame（first/last frame 補間）: veo-2.0 / veo-3.1 系（Fast も対応）。
//     Veo 3.0 系は非対応。
func ModelCapabilities(model string) Capabilities {
	m := strings.ToLower(strings.TrimSpace(model))
	return Capabilities{
		ReferenceImages: strings.HasPrefix(m, "veo-3") && !strings.Contains(m, "fast"),
		LastFrame:       strings.HasPrefix(m, "veo-2") || strings.HasPrefix(m, "veo-3.1"),
	}
}
