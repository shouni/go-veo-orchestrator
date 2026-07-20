package ports

import "testing"

func TestVideoRecipeSchemaExcludesPipelineFields(t *testing.T) {
	schema := VideoRecipeSchema(nil)

	for _, excluded := range []string{"music_recipe", "final_video_url", "aspect_ratio"} {
		if _, ok := schema.Properties[excluded]; ok {
			t.Errorf("schema.Properties[%q] should not be present (populated by the pipeline, not the AI)", excluded)
		}
	}

	cutSchema := schema.Properties["cuts"].Items
	for _, excluded := range []string{
		"cut_index", "section_index", "start_sec", "end_sec",
		"keyframe_reference", "video_url", "video_id", "status",
		"is_chain_start", "is_section_start",
	} {
		if _, ok := cutSchema.Properties[excluded]; ok {
			t.Errorf("cutSchema.Properties[%q] should not be present (populated by the pipeline, not the AI)", excluded)
		}
	}
}

func TestVideoRecipeSchemaCharacterIDEnumAllowsEmptyForScenery(t *testing.T) {
	schema := VideoRecipeSchema([]string{"zundamon", "metan"})

	enum := schema.Properties["cuts"].Items.Properties["character_id"].Enum
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
	schema := VideoRecipeSchema(nil)

	want := map[string]bool{"project_title": true, "cuts": true}
	if len(schema.Required) != len(want) {
		t.Fatalf("Required = %v, want keys %v", schema.Required, want)
	}
	for _, field := range schema.Required {
		if !want[field] {
			t.Errorf("unexpected required field %q", field)
		}
	}
}
