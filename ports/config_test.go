package ports

import (
	"errors"
	"testing"
)

func TestConfigWithModelsOverridesNonEmptyValues(t *testing.T) {
	cfg := Config{GeminiModel: "gemini-default", ImageModel: "image-default"}

	got := cfg.WithModels(" gemini-selected ", " image-selected ")

	if got.GeminiModel != "gemini-selected" {
		t.Fatalf("GeminiModel = %q, want gemini-selected", got.GeminiModel)
	}
	if got.ImageModel != "image-selected" {
		t.Fatalf("ImageModel = %q, want image-selected", got.ImageModel)
	}
}

func TestConfigWithModelsKeepsCurrentModelsForEmptyValues(t *testing.T) {
	cfg := Config{GeminiModel: "gemini-default", ImageModel: "image-default"}

	got := cfg.WithModels("", " ")

	if got.GeminiModel != "gemini-default" {
		t.Fatalf("GeminiModel = %q, want gemini-default", got.GeminiModel)
	}
	if got.ImageModel != "image-default" {
		t.Fatalf("ImageModel = %q, want image-default", got.ImageModel)
	}
}

func TestConfigUsesModels(t *testing.T) {
	cfg := Config{GeminiModel: "gemini-default", ImageModel: "image-default"}

	if !cfg.UsesModels("gemini-default", "image-default") {
		t.Fatal("UsesModels() = false, want true for same models")
	}
	if cfg.UsesModels("gemini-alt", "image-default") {
		t.Fatal("UsesModels() = true, want false for different gemini model")
	}
	if cfg.UsesModels("gemini-default", "image-alt") {
		t.Fatal("UsesModels() = true, want false for different image model")
	}
}

// TestConfigValidateRequiresArtDirection pins that the library holds no art-direction
// defaults. Silently falling back to a kit constant would give the caller's own setting
// (ap-mv's VEO_ASPECT_RATIO) a second source of truth, and changing one would leave the
// other behind — keyframes at 16:9 for a 9:16 short, with nothing in the logs.
func TestConfigValidateRequiresArtDirection(t *testing.T) {
	for name, mutate := range map[string]func(*Config){
		"aspect ratio": func(c *Config) { c.KeyframeAspectRatio = "" },
		"image size":   func(c *Config) { c.KeyframeImageSize = "  " },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := Config{
				GeminiModel:         "gemini-text",
				ImageModel:          "gemini-image",
				KeyframeAspectRatio: "16:9",
				KeyframeImageSize:   "2K",
			}
			mutate(&cfg)
			if err := cfg.Validate(); !errors.Is(err, ErrConfigInvalid) {
				t.Errorf("Validate() = %v, want ErrConfigInvalid", err)
			}
		})
	}
}

// KeyframeNegativePrompt は必須ではありません。「排除指定なし」は意味のある選択で、
// 空を弾くとネガティブプロンプト無しの生成ができなくなるためです。
func TestConfigValidateAllowsEmptyNegativePrompt(t *testing.T) {
	cfg := Config{
		GeminiModel:         "gemini-text",
		ImageModel:          "gemini-image",
		KeyframeAspectRatio: "16:9",
		KeyframeImageSize:   "2K",
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}
