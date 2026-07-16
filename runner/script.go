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

	"github.com/shouni/go-gemini-client/gemini"
	"github.com/shouni/go-veo-orchestrator/ports"
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
	aiClient      gemini.ContentGenerator
	reader        ports.ContentReader
	aiModel       string
}

// NewVideoScriptRunner は依存関係を注入して初期化します。
func NewVideoScriptRunner(
	pb ports.ScriptPrompt,
	ai gemini.ContentGenerator,
	r ports.ContentReader,
	aiModel string,
) *VideoScriptRunner {
	return &VideoScriptRunner{
		promptBuilder: pb,
		aiClient:      ai,
		reader:        r,
		aiModel:       aiModel,
	}
}

// Run は Music Recipe JSON を読み込み、Gemini を用いて動画台本 JSON を生成します。
func (r *VideoScriptRunner) Run(ctx context.Context, sourceURL string, mode string) (*ports.VideoRecipe, error) {
	slog.Info("ScriptRunner: 処理を開始", "url", sanitizeURL(sourceURL))

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

	// 3. Gemini API を呼び出し
	slog.Info("ScriptRunner: Gemini APIを呼び出し中", "model", r.aiModel)
	resp, err := r.aiClient.GenerateContent(ctx, r.aiModel, finalPrompt)
	if err != nil {
		return nil, fmt.Errorf("geminiによるコンテンツ生成に失敗しました: %w", err)
	}
	if resp == nil || strings.TrimSpace(resp.Text) == "" {
		return nil, fmt.Errorf("AIクライアントが空の応答を返しました: %w", ports.ErrInvalidAIResponse)
	}

	// 4. AI の応答をパース
	recipe, err := r.parseResponse(resp.Text)
	if err != nil {
		return nil, err
	}

	return recipe, nil
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
func (r *VideoScriptRunner) parseResponse(raw string) (*ports.VideoRecipe, error) {
	candidates := extractJSONCandidates(raw)
	if len(candidates) == 0 {
		slog.Warn("AIの応答からJSONを抽出できませんでした。応答全体を対象にパースを試みます。",
			"response_snippet", truncateString(raw, 100))
		candidates = []string{raw}
	}

	var parseErr error
	for _, jsonStr := range candidates {
		var recipe ports.VideoRecipe
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

func parseSourceRecipe(raw string) (*ports.VideoRecipe, error) {
	var parseErr error
	for _, jsonStr := range extractJSONCandidates(raw) {
		var recipe ports.VideoRecipe
		if err := json.Unmarshal([]byte(jsonStr), &recipe); err == nil && hasVideoRecipeContent(&recipe) {
			recipe.Normalize()
			return &recipe, nil
		}

		var musicRecipe ports.MusicRecipe
		if err := json.Unmarshal([]byte(jsonStr), &musicRecipe); err != nil {
			if parseErr == nil {
				parseErr = err
			}
			continue
		}
		if !hasMusicRecipeContent(&musicRecipe) {
			continue
		}

		newRecipe := ports.VideoRecipe{
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

func hasVideoRecipeContent(recipe *ports.VideoRecipe) bool {
	return recipe.ProjectTitle != "" ||
		recipe.Description != "" ||
		len(recipe.Cuts) > 0 ||
		hasMusicRecipeContent(&recipe.MusicRecipe)
}

func hasMusicRecipeContent(recipe *ports.MusicRecipe) bool {
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
