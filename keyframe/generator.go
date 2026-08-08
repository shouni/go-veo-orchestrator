// Package keyframe は、カット情報とキャラクター定義から動画のキーフレーム画像を
// 生成するロジックを提供します。参照画像の解決（GCS 直接参照か File API へのアップロードか）と
// レート制限・リクエストタイムアウトは gemini-image-kit が担うため、このパッケージは
// カットごとの参照元 URL とプロンプトの組み立てに専念します。
package keyframe

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	characterkit "github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-gemini-client/gemini"

	"github.com/shouni/go-veo-orchestrator/ports"
)

// defaultNegativeKeyframePrompt は単体カットのキーフレームで「文字」や「フキダシ」を
// 排除するための既定の指定です（WithNegativePrompt で差し替え可能）。
const defaultNegativeKeyframePrompt = "speech bubble, dialogue balloon, text, alphabet, letters, words, signatures, watermark, username, low quality, distorted, bad anatomy, monochrome, black and white, greyscale"

// Generator は、キャラクターの一貫性を保ちながら複数カットのキーフレームを生成します。
//
// レート制限・並列度・タイムアウトは注入される画像ジェネレータ
// （gemini-image-kit の WithRateLimit / WithMaxConcurrency / WithRequestTimeout）が
// 受け持ちます。以前はこのパッケージが errgroup + rate.Limiter を自前で持っており、
// 同じガードが画像キットの下流3箇所に重複していました。
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
	cut   ports.Cut
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

	applyDefaultOptions(g)
	for _, opt := range opts {
		opt(g)
	}

	return g
}

// Execute は、カット列のキーフレームを順に生成します。
//
// 戻り値は cuts と同じ長さ・並びで、エラー時も生成できた位置には結果が入ります。
// キーフレーム 1 枚ごとに生成コストが掛かるため、1 件の失敗で支払い済みの画像を
// 捨てません（呼び出し側の RunAndSave は部分結果も保存するので、再実行は失敗した
// カットだけを続きから生成します）。エラーは errors.Join で集約されます。
func (g *Generator) Execute(ctx context.Context, cuts []ports.Cut) ([]*ports.KeyframeImage, error) {
	if len(cuts) == 0 {
		return nil, nil
	}

	images := make([]*ports.KeyframeImage, len(cuts))
	errs := make([]error, len(cuts))

	for i, cut := range cuts {
		task := keyframeTask{index: i, total: len(cuts), cut: cut}
		// 呼び出し元のキャンセル後は新しい生成を始めない（生成済みの結果は保持する）。
		if err := ctx.Err(); err != nil {
			errs[i] = err
			continue
		}
		images[i], errs[i] = g.generateCutKeyframe(ctx, task)
	}

	return images, errors.Join(errs...)
}

func (g *Generator) generateCutKeyframe(ctx context.Context, task keyframeTask) (*ports.KeyframeImage, error) {
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

// runImageGeneration wraps a single Generate call with the start/complete logging and
// error message shape shared by generateCutKeyframe and EditCut.
func (g *Generator) runImageGeneration(
	ctx context.Context,
	req imagePorts.ImageRequest,
	logger *slog.Logger,
	startLog, completeLog, actionJP string,
	cutIndex int,
	characterID string,
) (*ports.KeyframeImage, error) {
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
func keyframeImageFrom(resp *imagePorts.ImageResponse) *ports.KeyframeImage {
	if resp == nil {
		return nil
	}
	return &ports.KeyframeImage{
		Data:     resp.Data,
		MimeType: resp.MimeType,
		UsedSeed: resp.UsedSeed,
		Model:    resp.Model,
		Prompt:   resp.Prompt,
	}
}

// EditCut edits an existing keyframe image for a single cut using a text instruction
// (cut.KeyframeReference as the source image), preserving composition/pose/background and
// changing only what editPrompt specifies. It reuses the same conversational image model as
// Execute (Gemini's multimodal "Nano Banana" image models support editing an input image via
// a plain generateContent call), rather than a dedicated edit API — Vertex AI's Imagen
// mask-based edit/capability models have no supported successor.
func (g *Generator) EditCut(ctx context.Context, cut ports.Cut, editPrompt string) (*ports.KeyframeImage, error) {
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
func (g *Generator) characterForCut(cut ports.Cut) *characterkit.Character {
	return g.characters.GetCharacterWithDefault(cut.CharacterID)
}

func (g *Generator) buildImageRequest(cut ports.Cut, char *characterkit.Character) imagePorts.ImageRequest {
	userPrompt, systemPrompt := g.pb.BuildCut(cut, char)
	// キャラクターの参照画像が生成対象と異なるアスペクト比（例: 横長3ポーズシートを縦長
	// キーフレームの参照に使う）だと、色・小物配置・髪型などの細部が生成のたびにブレやすいため、
	// g.aspectRatio に一致する参照画像（ReferenceURLs）があればそちらを優先します。
	referenceURL := char.ReferenceURLFor(g.aspectRatio)

	// 参照の解決（Vertex AI + gs:// は直接参照、Gemini API は File API へ1回だけアップロード）は
	// gemini-image-kit が担う。ここは参照元 URL を渡すだけでよい。
	return imagePorts.ImageRequest{
		GenerationOptions: g.buildGenerationOptions(userPrompt, systemPrompt, char.Seed),
		Images:            []imagePorts.ImageURI{{ReferenceURL: referenceURL}},
	}
}

// buildGenerationOptions builds the GenerationOptions shared by buildImageRequest and EditCut;
// only the Images field differs between a fresh keyframe generation and an edit of an existing one.
func (g *Generator) buildGenerationOptions(prompt, systemPrompt string, seed *int64) imagePorts.GenerationOptions {
	return imagePorts.GenerationOptions{
		Model:          g.model,
		Prompt:         prompt,
		NegativePrompt: g.negativePrompt,
		GenerateOptions: gemini.GenerateOptions{
			SystemPrompt: systemPrompt,
			AspectRatio:  g.aspectRatio,
			ImageSize:    g.imageSize,
			Seed:         seed,
		},
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
