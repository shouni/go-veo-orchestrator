package video

import "testing"

// properties extracts properties from a JSON Schema object.
func properties(t *testing.T, schema map[string]any) map[string]any {
	t.Helper()

	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema has no properties: %v", schema)
	}
	return props
}

// cutProperties extracts the properties of a cuts[] element.
func cutProperties(t *testing.T, schema map[string]any) map[string]any {
	t.Helper()

	cuts, ok := properties(t, schema)["cuts"].(map[string]any)
	if !ok {
		t.Fatal("schema has no cuts property")
	}
	items, ok := cuts["items"].(map[string]any)
	if !ok {
		t.Fatal("cuts has no items schema")
	}
	return properties(t, items)
}

func TestVideoRecipeSchemaExcludesPipelineFields(t *testing.T) {
	schema := RecipeSchema(nil)

	props := properties(t, schema)
	for _, excluded := range []string{"music_recipe", "final_video_url", "aspect_ratio"} {
		if _, ok := props[excluded]; ok {
			t.Errorf("schema properties[%q] should not be present (populated by the pipeline, not the AI)", excluded)
		}
	}

	cutProps := cutProperties(t, schema)
	for _, excluded := range []string{
		"cut_index", "section_index", "start_sec", "end_sec",
		"keyframe_reference", "video_url", "video_id", "status",
		"is_chain_start", "is_section_start",
	} {
		if _, ok := cutProps[excluded]; ok {
			t.Errorf("cut properties[%q] should not be present (populated by the pipeline, not the AI)", excluded)
		}
	}
}

func TestVideoRecipeSchemaAllowsPerCutAudioReference(t *testing.T) {
	schema := RecipeSchema(nil)

	if _, ok := cutProperties(t, schema)["audio_reference"]; !ok {
		t.Error("cut properties[\"audio_reference\"] should be present so the model can copy a cut-specific GCS audio URI from the source recipe")
	}
}

func TestVideoRecipeSchemaCharacterIDEnumAllowsEmptyForScenery(t *testing.T) {
	schema := RecipeSchema([]string{"zundamon", "metan"})

	characterID, ok := cutProperties(t, schema)["character_id"].(map[string]any)
	if !ok {
		t.Fatal("cut properties has no character_id")
	}
	enum, ok := characterID["enum"].([]string)
	if !ok {
		t.Fatalf("character_id has no enum: %v", characterID)
	}

	want := map[string]bool{"": true, "zundamon": true, "metan": true}
	if len(enum) != len(want) {
		t.Fatalf("character_id enum = %v, want keys %v", enum, want)
	}
	for _, id := range enum {
		if !want[id] {
			t.Errorf("unexpected character_id enum value %q", id)
		}
	}
}

func TestVideoRecipeSchemaRequiresProjectTitleAndCuts(t *testing.T) {
	schema := RecipeSchema(nil)

	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatalf("schema has no required list: %v", schema)
	}

	want := map[string]bool{"project_title": true, "cuts": true}
	if len(required) != len(want) {
		t.Fatalf("required = %v, want keys %v", required, want)
	}
	for _, field := range required {
		if !want[field] {
			t.Errorf("unexpected required field %q", field)
		}
	}
}

// TestVideoRecipeSchemaIsPlainJSONSchema checks that the schema is a plain JSON Schema.
// Going back to genai.Schema would bring back an SDK dependency just for defining the schema.
func TestVideoRecipeSchemaIsPlainJSONSchema(t *testing.T) {
	schema := RecipeSchema(nil)

	if got := schema["type"]; got != "object" {
		t.Errorf("schema type = %v, want %q", got, "object")
	}
	if got := properties(t, schema)["project_title"].(map[string]any)["type"]; got != "string" {
		t.Errorf("project_title type = %v, want %q", got, "string")
	}
}
