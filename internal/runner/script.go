package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"regexp"
	"strings"

	characterkit "github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-gemini-client/gemini"
	"github.com/shouni/go-veo-orchestrator/ports"
	"github.com/shouni/go-veo-orchestrator/video"
)

const (
	// maxInputSize は読み込みを許可する最大テキストサイズ (5MB) です。
	maxInputSize = 5 * 1024 * 1024
	// maxErrorResponseLength はエラーログに含める応答抜粋の最大文字数です。
	maxErrorResponseLength = 200
)

// jsonBlockRegex は、Markdown 形式の JSON ブロックを抽出するための正規表現です。
// 非貪欲マッチにより、複数のコードブロックが含まれる応答でも個別に抽出できます。
var jsonBlockRegex = regexp.MustCompile("(?s)```(?:json)?\\s*(.*?\\S)\\s*```")

// VideoScriptRunner は入力ソースから Music Recipe を読み取り、動画レシピを生成します。
type VideoScriptRunner struct {
	promptBuilder ports.ScriptPrompt
	aiClient      gemini.Generator
	reader        ports.ContentReader
	aiModel       string
	characters    *characterkit.Characters
}

// NewVideoScriptRunner は依存関係を注入して初期化します。
// characters は、AI に生成させる cuts[].character_id を既知のキャラクターIDに
// 制約する（ResponseSchema の enum）ために使われます。nil の場合は制約なし
// （空文字のみ許容）になります。
func NewVideoScriptRunner(
	pb ports.ScriptPrompt,
	ai gemini.Generator,
	r ports.ContentReader,
	aiModel string,
	characters *characterkit.Characters,
) *VideoScriptRunner {
	return &VideoScriptRunner{
		promptBuilder: pb,
		aiClient:      ai,
		reader:        r,
		aiModel:       aiModel,
		characters:    characters,
	}
}

// characterIDs は、スキーマの character_id enum に使うキャラクターID一覧を返します。
func (r *VideoScriptRunner) characterIDs() []string {
	if r.characters == nil {
		return nil
	}
	ids := make([]string, 0, len(r.characters.List))
	for _, c := range r.characters.List {
		ids = append(ids, c.ID)
	}
	return ids
}

// Run は Music Recipe JSON を読み込み、Gemini を用いて動画台本 JSON を生成します。
func (r *VideoScriptRunner) Run(ctx context.Context, sourceURL string, mode string) (*video.Recipe, error) {
	slog.InfoContext(ctx, "ScriptRunner: 処理を開始", "url", sanitizeURL(sourceURL))

	// 1. ソースからテキストを取得
	inputText, err := r.readContent(ctx, sourceURL)
	if err != nil {
		return nil, err
	}

	// 2. プロンプトの構築
	sourceRecipe, err := parseSourceRecipe(inputText)
	if err != nil {
		return nil, err
	}
	if sourceRecipe == nil {
		return nil, fmt.Errorf("sourceURL の内容を Music Recipe JSON として解析できませんでした")
	}
	data := ports.TemplateData{
		SourceRecipe: sourceRecipe,
	}
	finalPrompt, err := r.promptBuilder.Build(mode, &data)
	if err != nil {
		return nil, fmt.Errorf("プロンプトの構築に失敗しました: %w", err)
	}

	// 3. Gemini API を呼び出し、使える台本が得られるまで作り直す
	return r.generateRecipe(ctx, finalPrompt, sourceRecipe)
}

// maxScriptAttempts は、検証に落ちた台本を作り直す最大回数です。
//
// 1 回で諦めると生成のゆらぎがそのままジョブ失敗になり、無制限に粘ると台本生成の課金だけが
// 積み上がります。数回試して直らないなら、プロンプトかスキーマ側が壊れています。
const maxScriptAttempts = 3

// generateRecipe は台本を生成し、後段が扱える構造になるまでやり直します。
//
// JSON Schema は型と必須項目までしか縛れないため、cuts が空になる、section_index が
// 音楽のセクション数を超える、といった破綻は素通りします。ここで弾かないと、
// パイプラインで最も高価な Veo のカット生成まで進んでから失敗します。
func (r *VideoScriptRunner) generateRecipe(ctx context.Context, finalPrompt string, sourceRecipe *video.Recipe) (*video.Recipe, error) {
	opts := gemini.GenerateOptions{
		ResponseMIMEType:   "application/json",
		ResponseJSONSchema: video.RecipeSchema(r.characterIDs()),
	}

	var lastErr error
	for attempt := 1; attempt <= maxScriptAttempts; attempt++ {
		slog.InfoContext(ctx, "ScriptRunner: Gemini APIを呼び出し中", "model", r.aiModel, "attempt", attempt)

		resp, err := r.aiClient.GenerateWithAttachments(ctx, r.aiModel, finalPrompt, nil, opts)
		if err != nil {
			return nil, fmt.Errorf("geminiによるコンテンツ生成に失敗しました: %w", err)
		}
		if resp == nil || strings.TrimSpace(resp.Text) == "" {
			return nil, fmt.Errorf("AIクライアントが空の応答を返しました: %w", ports.ErrInvalidAIResponse)
		}
		if resp.Usage != nil {
			slog.InfoContext(ctx, "ScriptRunner: トークン使用量",
				"prompt_tokens", resp.Usage.PromptTokenCount,
				"candidates_tokens", resp.Usage.CandidatesTokenCount,
				"total_tokens", resp.Usage.TotalTokenCount,
			)
		}

		recipe, err := r.parseResponse(ctx, resp.Text)
		if err != nil {
			// JSON として読めない応答は作り直しても直る見込みが薄いので、そのまま返す。
			return nil, err
		}

		// music_recipe はスキーマから意図的に除外している（AIには生成させない）ため、
		// source 側の値をそのまま引き継ぐ。section_index の検証もこれが揃ってから行う。
		recipe.MusicRecipe = sourceRecipe.MusicRecipe
		recipe.Normalize()

		lastErr = recipe.Validate()
		if lastErr == nil {
			return recipe, nil
		}
		slog.WarnContext(ctx, "ScriptRunner: 生成された台本が不正なため作り直します",
			"attempt", attempt, "max_attempts", maxScriptAttempts, "err", lastErr)
	}

	return nil, fmt.Errorf("生成された台本が %d 回とも不正でした: %w", maxScriptAttempts, lastErr)
}

// readContent は、指定されたソースURLからコンテンツを取得します。
func (r *VideoScriptRunner) readContent(ctx context.Context, sourceURL string) (string, error) {
	rc, err := r.reader.Open(ctx, sourceURL)
	if err != nil {
		return "", fmt.Errorf("failed to read source: %w", err)
	}
	defer func() {
		if closeErr := rc.Close(); closeErr != nil {
			slog.WarnContext(ctx, "ストリームのクローズに失敗しました", "error", closeErr)
		}
	}()
	limitedReader := io.LimitReader(rc, int64(maxInputSize))
	content, err := io.ReadAll(limitedReader)
	if err != nil {
		return "", fmt.Errorf("読み込みに失敗しました: %w", err)
	}

	// 追加の読み込みを試みてサイズ超過を判定
	oneMoreByte := make([]byte, 1)
	n, readErr := rc.Read(oneMoreByte)
	if readErr != nil && readErr != io.EOF {
		return "", fmt.Errorf("サイズ確認中にエラーが発生しました: %w", readErr)
	}

	if n > 0 {
		return "", fmt.Errorf("入力が上限 %d bytes を超えています (url: %s): %w",
			maxInputSize, sanitizeURL(sourceURL), ports.ErrInputTooLarge)
	}

	return strings.ToValidUTF8(string(content), ""), nil
}

// parseResponse は AI の応答から JSON 候補を抽出し、動画レシピとして内容を持つ
// 最初の候補を採用します。説明用の JSON ブロックが混在する応答にも対応します。
func (r *VideoScriptRunner) parseResponse(ctx context.Context, raw string) (*video.Recipe, error) {
	candidates := extractJSONCandidates(raw)
	if len(candidates) == 0 {
		slog.WarnContext(ctx, "AIの応答からJSONを抽出できませんでした。応答全体を対象にパースを試みます。",
			"response_snippet", truncateString(raw, 100))
		candidates = []string{raw}
	}

	var parseErr error
	for _, jsonStr := range candidates {
		var recipe video.Recipe
		if err := json.Unmarshal([]byte(jsonStr), &recipe); err != nil {
			if parseErr == nil {
				parseErr = err
			}
			continue
		}
		if !hasVideoRecipeContent(&recipe) {
			continue
		}
		recipe.Normalize()
		return &recipe, nil
	}

	if parseErr != nil {
		return nil, fmt.Errorf("AI応答JSONの解析に失敗しました (抜粋: %q): %w: %w",
			truncateString(raw, maxErrorResponseLength), ports.ErrInvalidAIResponse, parseErr)
	}
	return nil, fmt.Errorf("AI応答のJSONに動画レシピの内容が含まれていません (抜粋: %q): %w",
		truncateString(raw, maxErrorResponseLength), ports.ErrInvalidAIResponse)
}

func parseSourceRecipe(raw string) (*video.Recipe, error) {
	var parseErr error
	for _, jsonStr := range extractJSONCandidates(raw) {
		var recipe video.Recipe
		if err := json.Unmarshal([]byte(jsonStr), &recipe); err == nil && hasVideoRecipeContent(&recipe) {
			recipe.Normalize()
			return &recipe, nil
		}

		var musicRecipe video.MusicRecipe
		if err := json.Unmarshal([]byte(jsonStr), &musicRecipe); err != nil {
			if parseErr == nil {
				parseErr = err
			}
			continue
		}
		if !hasMusicRecipeContent(&musicRecipe) {
			continue
		}

		newRecipe := video.Recipe{
			MusicRecipe: musicRecipe,
		}
		newRecipe.Normalize()
		return &newRecipe, nil
	}

	if parseErr != nil {
		return nil, fmt.Errorf("source JSON の解析に失敗しました: %w", parseErr)
	}
	return nil, nil
}

func hasVideoRecipeContent(recipe *video.Recipe) bool {
	return recipe.ProjectTitle != "" ||
		recipe.Description != "" ||
		len(recipe.Cuts) > 0 ||
		hasMusicRecipeContent(&recipe.MusicRecipe)
}

func hasMusicRecipeContent(recipe *video.MusicRecipe) bool {
	return recipe.Title != "" ||
		recipe.Theme != "" ||
		recipe.Mood != "" ||
		recipe.Tempo != 0 ||
		len(recipe.Instruments) > 0 ||
		len(recipe.Sections) > 0 ||
		recipe.Lyrics != nil ||
		recipe.AudioModel != "" ||
		recipe.ComposeMode != "" ||
		recipe.Seed != nil
}

// extractJSONCandidates は文字列から JSON として解釈しうる候補を優先度順に返します。
// Markdown コードブロックを個別の候補として列挙し、最後に区切り文字ベースの
// 抽出結果をフォールバックとして加えます。
func extractJSONCandidates(raw string) []string {
	cleanRaw := strings.TrimSpace(raw)

	var candidates []string
	for _, matches := range jsonBlockRegex.FindAllStringSubmatch(cleanRaw, -1) {
		candidates = append(candidates, matches[1])
	}

	first := firstJSONDelimiter(cleanRaw)
	last := lastJSONDelimiter(cleanRaw)
	if first != -1 && last != -1 && last > first {
		candidates = append(candidates, cleanRaw[first:last+1])
	}

	return candidates
}

func firstJSONDelimiter(s string) int {
	firstObj := strings.Index(s, "{")
	firstArr := strings.Index(s, "[")
	if firstObj == -1 || (firstArr != -1 && firstArr < firstObj) {
		return firstArr
	}
	return firstObj
}

func lastJSONDelimiter(s string) int {
	lastObj := strings.LastIndex(s, "}")
	lastArr := strings.LastIndex(s, "]")
	if lastObj == -1 || (lastArr != -1 && lastArr > lastObj) {
		return lastArr
	}
	return lastObj
}

// sanitizeURL はログやエラーメッセージへの出力用に、URL からクエリパラメータと
// フラグメントを除去します。署名付き URL の認証情報が漏れることを防ぎます。
func sanitizeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "(unparsable url)"
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// truncateString は指定された長さで文字列を安全に切り捨てます。
func truncateString(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
