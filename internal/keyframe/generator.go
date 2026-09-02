// Package keyframe は、カット情報とキャラクター定義から動画のキーフレーム画像を
// 生成するロジックを提供します。参照画像は gs:// URI のまま genai-kit の imagegen へ
// 渡すため、このパッケージの仕事はカット 1 件分の参照元 URL とプロンプトの組み立て、
// そして 1 回の送信だけです。並列度は runner.CutKeyframeRunner が、発射間隔と 1 回
// あたりの上限時間は workflow の callGuard が持ちます。
package keyframe

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/shouni/genai-kit/gemini"
	"github.com/shouni/genai-kit/imagegen"
	characterkit "github.com/shouni/go-character-kit/character"

	"github.com/shouni/go-veo-orchestrator/ports"
	"github.com/shouni/go-veo-orchestrator/video"
)

// Generator は、キャラクターの一貫性を保ちながらカットのキーフレームを 1 枚ずつ生成します。
type Generator struct {
	characters     *characterkit.Characters
	generator      imagegen.Generator
	pb             ports.KeyframePrompt
	model          string
	aspectRatio    string
	imageSize      string
	negativePrompt string
}

type keyframeTask struct {
	index int
	// total は今回まとめて生成するキーフレームの総数です（進捗ログ用）。
	total int
	cut   video.Cut
}

// NewGenerator は Generator の新しいインスタンスを初期化します。
func NewGenerator(
	characters *characterkit.Characters,
	generator imagegen.Generator,
	pb ports.KeyframePrompt,
	model string,
	opts ...Option,
) *Generator {
	g := &Generator{
		characters: characters,
		generator:  generator,
		pb:         pb,
		model:      model,
	}

	for _, opt := range opts {
		opt(g)
	}

	return g
}

// GenerateCut は 1 カット分のキーフレームを生成します。並列実行はここでは行いません
// （呼び出し側が 1 枚ごとに保存できるようにするため — CutKeyframeRunner.GenerateAndSave 参照）。
// index / total は進捗ログ用（1 始まり）で、生成そのものには影響しません。
func (g *Generator) GenerateCut(ctx context.Context, cut video.Cut, index, total int) (*video.KeyframeImage, error) {
	return g.generateCutKeyframe(ctx, keyframeTask{index: index - 1, total: total, cut: cut})
}

func (g *Generator) generateCutKeyframe(ctx context.Context, task keyframeTask) (*video.KeyframeImage, error) {
	char := g.characterForCut(task.cut)
	if char == nil {
		return nil, fmt.Errorf("cut %d: キャラクターID '%s' に対応するキャラクターが見つかりません", task.cut.CutIndex, task.cut.CharacterID)
	}

	req := g.buildImageRequest(task.cut, char)
	logger := newKeyframeLogger(task, char, req.Images)

	// 進捗をメッセージ本文にも入れる。属性（keyframe_index / keyframe_total）にも
	// 同じ値があるが、Cloud Logging の折りたたみ表示ではメッセージしか見えず、
	// 1枚に分単位かかる処理では「何枚中の何枚目か」が読めないと進捗が判断できない。
	progress := fmt.Sprintf("(%d/%d)", task.index+1, task.total)
	return g.runImageGeneration(ctx, imageRun{
		req:         req,
		logger:      logger,
		startLog:    "Starting keyframe generation " + progress,
		completeLog: "Keyframe generation completed " + progress,
		actionJP:    "キーフレーム生成",
		cutIndex:    task.index + 1,
		characterID: char.ID,
	})
}

// imageRun は runImageGeneration の 1 回分の呼び出しを説明します。
//
// 位置引数だと startLog / completeLog / actionJP という無関係な文字列が 3 つ並び、
// 取り違えてもコンパイルが通ってしまうため、構造体で受け取ります
// （go-comic-kit の *RunnerArgs と同じ理由）。
type imageRun struct {
	req    imagegen.Request
	logger *slog.Logger
	// startLog / completeLog は開始・完了時のメッセージ本文です（完了側には所要時間が後置）。
	startLog, completeLog string
	// actionJP はエラー文言に埋める日本語の動作名です（「キーフレーム生成」など）。
	actionJP string
	// cutIndex / characterID はエラー文言に埋める識別子です。
	cutIndex    int
	characterID string
}

// runImageGeneration は 1 回の Generate 呼び出しを、開始・完了ログとエラー文言の
// 体裁で包みます。generateCutKeyframe と EditCut が共有します。
func (g *Generator) runImageGeneration(ctx context.Context, run imageRun) (*video.KeyframeImage, error) {
	run.logger.Info(run.startLog)
	startTime := time.Now()

	resp, err := g.generator.Generate(ctx, run.req)
	if err != nil {
		return nil, fmt.Errorf("cut %d (character_id: %s) の%sに失敗しました: %w",
			run.cutIndex, run.characterID, run.actionJP, err)
	}

	// 所要時間もメッセージへ入れる。生成が遅いとき、1枚ずつの実測が並ぶだけで当たりが付く。
	elapsed := time.Since(startTime).Round(time.Second)
	run.logger.Info(fmt.Sprintf("%s %s", run.completeLog, elapsed), "duration", elapsed)

	return keyframeImageFrom(resp), nil
}

// keyframeImageFrom は、imagegen のレスポンスをライブラリ自身の型へ写します。
// imagegen の型を公開 API へ晒さないための境界で、このパッケージだけが両方を知ります。
func keyframeImageFrom(resp *imagegen.Response) *video.KeyframeImage {
	if resp == nil {
		return nil
	}
	return &video.KeyframeImage{
		Data:     resp.Data,
		MimeType: resp.MIMEType,
		UsedSeed: resp.UsedSeed,
		Model:    resp.Model,
		Prompt:   resp.Prompt,
	}
}

// EditCut は、既存のキーフレーム画像（cut.KeyframeReference を元画像とする）を
// テキスト指示で編集します。構図・ポーズ・背景は保ったまま、editPrompt が指定した
// 部分だけを変えます。
//
// 専用の編集 API ではなく GenerateCut と同じ対話型の画像モデルを使います。Gemini の
// マルチモーダル画像モデル（Nano Banana 系）は通常の generateContent で入力画像の
// 編集ができ、Vertex AI の Imagen マスク編集系モデルには後継が無いためです。
func (g *Generator) EditCut(ctx context.Context, cut video.Cut, editPrompt string) (*video.KeyframeImage, error) {
	if cut.KeyframeReference == "" {
		return nil, fmt.Errorf("cut %d: %w", cut.CutIndex, ports.ErrNoKeyframeToEdit)
	}

	char := g.characterForCut(cut)
	if char == nil {
		return nil, fmt.Errorf("cut %d: キャラクターID '%s' に対応するキャラクターが見つかりません", cut.CutIndex, cut.CharacterID)
	}

	userPrompt, systemPrompt := g.pb.BuildEdit(cut, char, editPrompt)
	req := g.buildRequest(userPrompt, systemPrompt, char.Seed, cut.KeyframeReference)

	logger := slog.With(
		"cut_index", cut.CutIndex,
		"character_id", char.ID,
		"character_name", char.Name,
	)

	return g.runImageGeneration(ctx, imageRun{
		req:         req,
		logger:      logger,
		startLog:    "Starting keyframe edit",
		completeLog: "Keyframe edit completed",
		actionJP:    "キーフレーム編集",
		cutIndex:    cut.CutIndex,
		characterID: char.ID,
	})
}

// characterForCut はカットに対応するキャラクターを解決します。
// cut.CharacterID が未設定、または未知の ID の場合はデフォルトキャラクターに落とします。
func (g *Generator) characterForCut(cut video.Cut) *characterkit.Character {
	return g.characters.GetCharacterWithDefault(cut.CharacterID)
}

func (g *Generator) buildImageRequest(cut video.Cut, char *characterkit.Character) imagegen.Request {
	userPrompt, systemPrompt := g.pb.BuildCut(cut, char)
	// キャラクターの参照画像が生成対象と異なるアスペクト比（例: 横長3ポーズシートを縦長
	// キーフレームの参照に使う）だと、色・小物配置・髪型などの細部が生成のたびにブレやすいため、
	// g.aspectRatio に一致する参照画像（ReferenceURLs）があればそちらを優先します。
	referenceURL := char.ReferenceURLFor(g.aspectRatio)

	return g.buildRequest(userPrompt, systemPrompt, char.Seed, referenceURL)
}

// buildRequest は buildImageRequest と EditCut が共有するリクエストを組み立てます。
// 新規生成と既存画像の編集で違うのは参照画像だけです。
//
// referenceURL が空でも落としません。imagegen が参照先を持たない要素を送信対象から
// 外すため、「このキャラクターには参照画像が無い」をそのまま表現できます。
//
// 生成パラメータを型名付きの GenerateOptions リテラルで書いているのは、埋め込みで昇格した
// フィールドを複合リテラルのキーにできないためです（型名を省ける、という IDE の指摘は誤りで、
// 省くと missing type in composite literal になります）。
func (g *Generator) buildRequest(prompt, systemPrompt string, seed *int64, referenceURL string) imagegen.Request {
	return imagegen.Request{
		Model:          g.model,
		Prompt:         prompt,
		NegativePrompt: g.negativePrompt,
		Images:         []string{referenceURL},
		GenerateOptions: gemini.GenerateOptions{
			SystemPrompt: systemPrompt,
			AspectRatio:  g.aspectRatio,
			ImageSize:    g.imageSize,
			Seed:         seed,
		},
	}
}

func newKeyframeLogger(task keyframeTask, char *characterkit.Character, images []string) *slog.Logger {
	referenceURL := ""
	if len(images) > 0 {
		referenceURL = images[0]
	}
	return slog.With(
		"keyframe_index", task.index+1,
		"keyframe_total", task.total,
		"character_id", char.ID,
		"character_name", char.Name,
		"seed", char.Seed,
		"has_reference", referenceURL != "",
	)
}
