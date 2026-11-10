package video

// RecipeSchema is the response schema VideoScriptRunner uses to have Gemini
// produce structured output. Combined with ResponseMIMEType "application/json",
// it constrains the model output to this schema at the grammar level.
//
// music_recipe / final_video_url / aspect_ratio, and on Cut, cut_index /
// section_index / start_sec / end_sec / location_anchor / aspect_ratio /
// keyframe_reference / video_url / video_id / status / is_chain_start /
// is_section_start, are all values computed or attached by later pipeline stages
// (Recipe.Normalize, CutKeyframeRunner, VideoTimelineRunner), not something for
// the AI to generate, so they are deliberately left out of the schema. The cut's
// location_anchor / aspect_ratio in particular are copies Recipe.Normalize makes
// of the recipe-level fields of the same name, so the AI must not be allowed to
// write separate values for them.
//
// characterIDs lists the IDs of the character definitions
// (characterkit.Characters) valid for this call. Restricting cuts[].character_id
// to known IDs prevents, at the grammar level, the hallucination where the AI
// invents a character that does not exist. The empty string is also a valid value,
// to allow scenery cuts with no character in them.
//
// The return value is a plain JSON Schema (the form passed to
// gemini.GenerateOptions.ResponseJSONSchema). Building it with genai.Schema would
// tie code that merely writes a schema to the SDK's types, undercutting the point
// of going through genai-kit.
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

// cutSchema is the schema of an element of RecipeSchema's cuts[].
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
			// audio_reference normally stays empty and is filled in later with the
			// job-wide audio (a completion step on the caller's side). It is exposed here
			// so the model can copy the GCS URI when the source recipe points each cut
			// at a different segment.
			"audio_reference": map[string]any{"type": "string"},
		},
		"required": []string{"visual_anchor", "character_id"},
	}
}
