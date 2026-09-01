package runner

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// 本ファイルは「引用した本文に生の改行が混ざってもレシピが失われない」ことを固定します。
//
// 構造化出力（RecipeSchema）を指定しても、モデルは**歌詞や情景描写を引用するとき、
// JSON 文字列の中へ生の改行を入れてきます。** 応答を返しきったあとの崩れなので API の
// 再試行では直らず、補修が無ければレシピ生成が ErrInvalidAIResponse で丸ごと落ちます。
// 補修は genai-kit の gemini.CleanJSONResponse が持っており、
// extractJSONCandidates がそこを通します。

// recipeJSONWithRawNewline は description に生の改行を含むレシピ応答です。
// バッククォートではなく通常の文字列リテラルで書いているのは、\n を
// **エスケープではなく実際の改行**としてペイロードへ入れるためです。
const recipeJSONWithRawNewline = "```json\n" +
	"{\n" +
	"  \"project_title\": \"夜明けのデプロイ\",\n" +
	"  \"description\": \"1番: 静かな朝\n2番: 走り出す\",\n" +
	"  \"cuts\": []\n" +
	"}\n" +
	"```"

func TestParseResponseSurvivesRawNewlinesInQuotedText(t *testing.T) {
	t.Parallel()

	// 前提の固定: このペイロードは素のままでは JSON として読めません。
	// ここが通ってしまうと、以降は補修を通らずに成功するので試験になりません。
	if json.Valid([]byte(recipeJSONWithRawNewline)) {
		t.Fatal("試験用ペイロードが妥当な JSON になっています。生の改行が消えていないか確認してください")
	}

	r := &VideoScriptRunner{}
	recipe, err := r.parseResponse(context.Background(), recipeJSONWithRawNewline)
	if err != nil {
		t.Fatalf("引用文中の生の改行でレシピが失われました: %v", err)
	}

	if recipe.ProjectTitle != "夜明けのデプロイ" {
		t.Errorf("ProjectTitle = %q", recipe.ProjectTitle)
	}
	// 改行はエスケープされて本文に残る（削られない）。
	if !strings.Contains(recipe.Description, "\n") {
		t.Errorf("Description から改行が失われています: %q", recipe.Description)
	}
}
