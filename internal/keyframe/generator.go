// Package keyframe は、カット情報とキャラクター定義から動画のキーフレーム画像を
// 生成するロジックを提供します。参照画像は gs:// URI のまま gemini-image-kit へ
// 渡すため、このパッケージはカットごとの参照元 URL とプロンプトの組み立て、および
// カット間の並列実行に専念します。
package keyframe

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	characterkit "github.com/shouni/go-character-kit/character"

	"github.com/shouni/go-veo-orchestrator/ports"
	"github.com/shouni/go-veo-orchestrator/video"
)

// Generator は、キャラクターの一貫性を保ちながらカットのキーフレームを生成します。
//
// 生成の単位は 1 カットで、このパッケージに残るのは「1 枚をどう組み立ててどう送るか」
// だけです。並列度は runner.CutKeyframeRunner が、発射間隔と 1 回あたりの上限時間は
// workflow の callGuard が持ちます。
type Generator struct {
	characters     *characterkit.Characters
	generator      imagePorts.ImageGenerator
	pb             ports.KeyframePrompt
	model          string
	aspectRatio    string
	imageSize      string
	negativePrompt string
}

type keyframeTask struct {
	index int
	// total は今回まとめて生成するキーフレームの総数です。1枚ごとのログに「何枚中の
	// 何枚目か」を出すために持ちます。1枚に分単位で掛かることもあるため、総数が
	// 無いと進捗が読めません。
	total int
	cut   video.Cut
}

// NewGenerator は Generator の新しいインスタンスを初期化します。
func NewGenerator(
	characters *characterkit.Characters,
	generator imagePorts.ImageGenerator,
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
//
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
	return g.runImageGeneration(ctx, req, logger,
		"Starting keyframe generation "+progress, "Keyframe generation completed "+progress,
		"キーフレーム生成", task.index+1, char.ID)
}

// runImageGeneration は 1 回の Generate 呼び出しを、開始・完了ログとエラー文言の
// 体裁で包みます。generateCutKeyframe と EditCut が共有します。
func (g *Generator) runImageGeneration(
	ctx context.Context,
	req imagePorts.ImageRequest,
	logger *slog.Logger,
	startLog, completeLog, actionJP string,
	cutIndex int,
	characterID string,
) (*video.KeyframeImage, error) {
	logger.Info(startLog)
	startTime := time.Now()

	resp, err := g.generator.Generate(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("cut %d (character_id: %s) の%sに失敗しました: %w", cutIndex, characterID, actionJP, err)
	}

	// 所要時間もメッセージへ入れる（属性だけだと折りたたみ表示で見えないため）。
	// 生成が遅いときに、1枚ずつの実測が並ぶだけで当たりが付けられる。
	elapsed := time.Since(startTime).Round(time.Second)
	logger.Info(fmt.Sprintf("%s %s", completeLog, elapsed), "duration", elapsed)

	return keyframeImageFrom(resp), nil
}

// keyframeImageFrom は、画像キットのレスポンスをライブラリ自身の型へ写します。
// image-kit の型を公開 API へ晒さないための境界で、このパッケージだけが両方を知ります。
func keyframeImageFrom(resp *imagePorts.ImageResponse) *video.KeyframeImage {
	if resp == nil {
		return nil
	}
	return &video.KeyframeImage{
		Data:     resp.Data,
		MimeType: resp.MimeType,
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
	req := imagePorts.ImageRequest{
		GenerationOptions: g.buildGenerationOptions(userPrompt, systemPrompt, char.Seed),
		Images:            []imagePorts.ImageURI{{ReferenceURL: cut.KeyframeReference}},
	}

	logger := slog.With(
		"cut_index", cut.CutIndex,
		"character_id", char.ID,
		"character_name", char.Name,
	)

	return g.runImageGeneration(ctx, req, logger,
		"Starting keyframe edit", "Keyframe edit completed", "キーフレーム編集",
		cut.CutIndex, char.ID)
}

// characterForCut はカットに対応するキャラクターを解決します。
// cut.CharacterID が未設定、または未知の ID の場合はデフォルトキャラクターに落とします。
func (g *Generator) characterForCut(cut video.Cut) *characterkit.Character {
	return g.characters.GetCharacterWithDefault(cut.CharacterID)
}

func (g *Generator) buildImageRequest(cut video.Cut, char *characterkit.Character) imagePorts.ImageRequest {
	userPrompt, systemPrompt := g.pb.BuildCut(cut, char)
	// キャラクターの参照画像が生成対象と異なるアスペクト比（例: 横長3ポーズシートを縦長
	// キーフレームの参照に使う）だと、色・小物配置・髪型などの細部が生成のたびにブレやすいため、
	// g.aspectRatio に一致する参照画像（ReferenceURLs）があればそちらを優先します。
	referenceURL := char.ReferenceURLFor(g.aspectRatio)

	return imagePorts.ImageRequest{
		GenerationOptions: g.buildGenerationOptions(userPrompt, systemPrompt, char.Seed),
		Images:            []imagePorts.ImageURI{{ReferenceURL: referenceURL}},
	}
}

// buildGenerationOptions は buildImageRequest と EditCut が共有する GenerationOptions を
// 組み立てます。新規生成と既存画像の編集で違うのは Images だけです。
func (g *Generator) buildGenerationOptions(prompt, systemPrompt string, seed *int64) imagePorts.GenerationOptions {
	return imagePorts.GenerationOptions{
		Model:          g.model,
		Prompt:         prompt,
		NegativePrompt: g.negativePrompt,
		SystemPrompt:   systemPrompt,
		AspectRatio:    g.aspectRatio,
		ImageSize:      g.imageSize,
		Seed:           seed,
	}
}

func newKeyframeLogger(task keyframeTask, char *characterkit.Character, images []imagePorts.ImageURI) *slog.Logger {
	referenceURL := ""
	if len(images) > 0 {
		referenceURL = images[0].ReferenceURL
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
