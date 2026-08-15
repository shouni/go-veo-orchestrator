package video

// RecipeSchema は VideoScriptRunner が Gemini に構造化出力させる際の
// レスポンススキーマです。ResponseMIMEType "application/json" と併用することで、
// モデル出力が文法レベルでこのスキーマに制約されます。
//
// music_recipe / final_video_url / aspect_ratio、および Cut のうち cut_index /
// section_index / start_sec / end_sec / keyframe_reference / video_url /
// video_id / status / is_chain_start / is_section_start は、いずれもパイプラインの
// 後段（Recipe.Normalize、CutKeyframeRunner、VideoTimelineRunner）が算出・
// 付与する値であり、AI に生成させる対象ではないため意図的にスキーマへ含めません。
//
// characterIDs は、この呼び出しで有効なキャラクター定義（characterkit.Characters）
// の ID 一覧です。cuts[].character_id を既知の ID に限定することで、存在しない
// キャラクターを AI が作文してしまうハルシネーションを文法レベルで防ぎます。
// キャラクターの映らない情景カットを許容するため、空文字列も有効値に含みます。
//
// 戻り値は素の JSON Schema（gemini.GenerateOptions.ResponseJSONSchema へ渡す形）です。
// genai.Schema で組み立てると、スキーマを書くだけのコードが SDK の型に縛られ、
// go-gemini-client を挟んでいる意味が薄れます。
func RecipeSchema(characterIDs []string) map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"project_title":   map[string]any{"type": "string"},
			"description":     map[string]any{"type": "string"},
			"location_anchor": map[string]any{"type": "string"},
			"cuts": map[string]any{
				"type":  "array",
				"items": cutSchema(characterIDs),
			},
		},
		"required": []string{"project_title", "cuts"},
	}
}

// cutSchema は RecipeSchema の cuts[] 要素のスキーマです。
func cutSchema(characterIDs []string) map[string]any {
	characterIDEnum := append([]string{""}, characterIDs...)

	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"visual_anchor": map[string]any{"type": "string"},
			"character_id": map[string]any{
				"type": "string",
				"enum": characterIDEnum,
			},
			"dialogue":     map[string]any{"type": "string"},
			"audio_cue":    map[string]any{"type": "string"},
			"duration_sec": map[string]any{"type": "number"},
			// AudioReference is normally left empty and backfilled later from the job's shared
			// audio track (see the caller's cut-audio backfill step); it is exposed here only so
			// the model can copy a cut-specific GCS audio URI when the source recipe explicitly
			// calls for a different segment per cut.
			"audio_reference": map[string]any{"type": "string"},
		},
		"required": []string{"visual_anchor", "character_id"},
	}
}
