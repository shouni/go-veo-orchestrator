// Package ports defines the interfaces (ports) that the components of
// go-veo-orchestrator depend on, together with the shared configuration for video generation.
package ports

import (
	"fmt"
	"strings"
	"time"
)

// DefaultMaxConcurrency and the other constants are the defaults applied to Config.
const (
	DefaultMaxConcurrency = 1
	// DefaultRequestTimeout is the default upper bound on the time of a single AI call.
	// Image generation can take minutes, so the value leaves plenty of room.
	DefaultRequestTimeout = 5 * time.Minute
)

// Config is the basic configuration that the Go Veo Orchestrator Runners run on.
//
// MaxConcurrency / RateInterval / RequestTimeout control how AI calls are executed.
// The call interval and the time limit apply to both the script's text generation
// and the keyframes' image generation (quota is per project, not per kind of
// operation). Concurrency affects keyframe generation only; video generation is
// always sequential because of the Video-to-Video chain.
type Config struct {
	// --- AI Model Settings (Common) ---
	// Both are required (see Validate).
	GeminiModel string
	ImageModel  string

	// --- Generation Settings ---
	// MaxConcurrency is the number of keyframe generations run at once (0 or
	// less means the default, 1).
	//
	// Note: when RateInterval is set, throughput is capped at 1/RateInterval
	// regardless of concurrency. Setting both aggressively is contradictory.
	MaxConcurrency int
	// RateInterval is the minimum interval between AI calls (0 means unlimited).
	RateInterval time.Duration
	// RequestTimeout is the upper bound on the time of a single AI call (0 or
	// less means the default). The rate-limit wait happens outside this bound,
	// so congestion never turns into a timeout.
	RequestTimeout time.Duration
	// --- Art Direction ---
	//
	// This library holds no art-direction defaults at all. Aspect ratio,
	// resolution and negative prompt all change from work to work; a kit default
	// would be a second source next to the caller's own setting, and changing
	// only one of them would make the two disagree silently.

	// KeyframeAspectRatio is the aspect ratio for keyframe image generation
	// (e.g. "16:9", "9:16"). Required (see Validate). Sent empty, the image is
	// baked at whatever ratio the model picks, and the orientation changes
	// without anyone noticing.
	KeyframeAspectRatio string
	// KeyframeImageSize is the output resolution of keyframe images (e.g. "1K",
	// "2K"). Required (see Validate).
	KeyframeImageSize string
	// KeyframeNegativePrompt lists elements to exclude from keyframe generation
	// (e.g. "speech bubble, text, watermark").
	//
	// It is not required, because empty is a meaningful choice: "exclude
	// nothing". The kit holds no default wording, though, so state it explicitly
	// if you want to avoid text and speech bubbles. The wording stays out of the
	// kit because art style (monochrome, etc.) creeps into it, and then a work
	// meant to be monochrome could not be made without a kit release.
	KeyframeNegativePrompt string
}

// ApplyDefaults applies the defaults to unset (zero-valued) fields.
//
// RateInterval is not filled in. 0 means "no limit", and inserting a default
// would leave callers no way to choose unlimited explicitly.
func (c *Config) ApplyDefaults() {
	if c.MaxConcurrency <= 0 {
		c.MaxConcurrency = DefaultMaxConcurrency
	}
	if c.RequestTimeout <= 0 {
		c.RequestTimeout = DefaultRequestTimeout
	}
}

// Validate checks that the model names and the art-direction fields are set.
func (c *Config) Validate() error {
	missing := make([]string, 0, 4)
	if strings.TrimSpace(c.GeminiModel) == "" {
		missing = append(missing, "GeminiModel")
	}
	if strings.TrimSpace(c.ImageModel) == "" {
		missing = append(missing, "ImageModel")
	}
	if strings.TrimSpace(c.KeyframeAspectRatio) == "" {
		missing = append(missing, "KeyframeAspectRatio")
	}
	if strings.TrimSpace(c.KeyframeImageSize) == "" {
		missing = append(missing, "KeyframeImageSize")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: %s must be set (this library holds no default model names and no art direction)", ErrConfigInvalid, strings.Join(missing, ", "))
	}
	return nil
}

// WithModels returns a copy of the Config with the given model names overriding the current ones.
// An empty name leaves that model unchanged.
func (c Config) WithModels(geminiModel, imageModel string) Config {
	if geminiModel = strings.TrimSpace(geminiModel); geminiModel != "" {
		c.GeminiModel = geminiModel
	}
	if imageModel = strings.TrimSpace(imageModel); imageModel != "" {
		c.ImageModel = imageModel
	}
	c.ApplyDefaults()
	return c
}

// WithAspectRatio returns a copy of the Config with the given aspect ratio.
// An empty string means "unchanged".
func (c Config) WithAspectRatio(aspectRatio string) Config {
	if aspectRatio = strings.TrimSpace(aspectRatio); aspectRatio != "" {
		c.KeyframeAspectRatio = aspectRatio
	}
	return c
}

// UsesModels reports whether applying the given models would leave the Config's model selection unchanged.
func (c Config) UsesModels(geminiModel, imageModel string) bool {
	current := c
	current.ApplyDefaults()
	selected := c.WithModels(geminiModel, imageModel)
	return current.GeminiModel == selected.GeminiModel &&
		current.ImageModel == selected.ImageModel
}
