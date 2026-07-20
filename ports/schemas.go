package ports

import "google.golang.org/genai"

// VideoRecipeSchema は VideoScriptRunner が Gemini に構造化出力させる際の
// レスポンススキーマです。ResponseMIMEType "application/json" と併用することで、
// モデル出力が文法レベルでこのスキーマに制約されます。
//
// music_recipe / final_video_url / aspect_ratio、および Cut のうち cut_index /
// section_index / start_sec / end_sec / keyframe_reference / video_url /
// video_id / status / is_chain_start / is_section_start は、いずれもパイプラインの
// 後段（VideoRecipe.Normalize、CutKeyframeRunner、VideoTimelineRunner）が算出・
// 付与する値であり、AI に生成させる対象ではないため意図的にスキーマへ含めません。
//
// characterIDs は、この呼び出しで有効なキャラクター定義（characterkit.Characters）
// の ID 一覧です。cuts[].character_id を既知の ID に限定することで、存在しない
// キャラクターを AI が作文してしまうハルシネーションを文法レベルで防ぎます。
// キャラクターの映らない情景カットを許容するため、空文字列も有効値に含みます。
func VideoRecipeSchema(characterIDs []string) *genai.Schema {
	return &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"project_title":   {Type: genai.TypeString},
			"description":     {Type: genai.TypeString},
			"location_anchor": {Type: genai.TypeString},
			"cuts": {
				Type:  genai.TypeArray,
				Items: cutSchema(characterIDs),
			},
		},
		Required: []string{"project_title", "cuts"},
	}
}

// cutSchema は VideoRecipeSchema の cuts[] 要素のスキーマです。
func cutSchema(characterIDs []string) *genai.Schema {
	characterIDEnum := append([]string{""}, characterIDs...)

	return &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"visual_anchor": {Type: genai.TypeString},
			"character_id": {
				Type:   genai.TypeString,
				Format: "enum",
				Enum:   characterIDEnum,
			},
			"dialogue":     {Type: genai.TypeString},
			"audio_cue":    {Type: genai.TypeString},
			"duration_sec": {Type: genai.TypeNumber},
			// AudioReference is normally left empty and backfilled later from the job's shared
			// audio track (see the caller's cut-audio backfill step); it is exposed here only so
			// the model can copy a cut-specific GCS audio URI when the source recipe explicitly
			// calls for a different segment per cut.
			"audio_reference": {Type: genai.TypeString},
		},
		Required: []string{"visual_anchor", "character_id"},
	}
}
