package ports

import "errors"

// Sentinel errors that can be checked across packages. Callers use errors.Is to
// identify a specific failure and take a different course (fallback or retry)
// than for a generic error.

var (
	// ErrRecipeRequired is returned when nil is passed to an operation that
	// requires a VideoRecipe.
	ErrRecipeRequired = errors.New("video recipe is required")

	// ErrEditingNotSupported is returned by CutKeyframeRunner.EditAndSave when
	// the configured image generator does not implement keyframe editing
	// (EditCut). Callers can detect it and choose an alternative flow, such as
	// falling back to full regeneration.
	ErrEditingNotSupported = errors.New("configured image generator does not support keyframe editing")

	// ErrInvalidAIResponse is returned when the AI's response text could not be
	// parsed as VideoRecipe JSON. Callers can detect it and decide on a retry
	// separately from prompt or network failures.
	ErrInvalidAIResponse = errors.New("AI response could not be parsed as video recipe JSON")

	// ErrVideoRunnerNotConfigured is returned when Workflows.Video is called on
	// workflows built without a VideoRunner (Veo adapter).
	ErrVideoRunnerNotConfigured = errors.New("video runner is not configured")

	// ErrInputTooLarge is returned when the input source exceeds the maximum
	// allowed size. Callers can detect it and, unlike a parse failure, ask for
	// the input to be corrected (split, or the source revised).
	ErrInputTooLarge = errors.New("input source exceeds maximum allowed size")

	// ErrNoKeyframeToEdit is returned when the cut to edit has no existing
	// keyframe image (KeyframeReference) yet. Callers can detect it and have the
	// keyframe generated first, with GenerateAndSave or similar.
	ErrNoKeyframeToEdit = errors.New("cut has no existing keyframe to edit")

	// ErrUnsupportedCutDuration is returned when a cut's duration is not accepted
	// by the Veo generation mode the cut resolves to (see DurationsForMode).
	// Callers can detect it and treat it as a duration-planning mistake on the
	// recipe side rather than a Veo API or network failure (retrying will not
	// fix it).
	ErrUnsupportedCutDuration = errors.New("cut duration is not supported by the resolved Veo generation mode")

	// ErrVideoRunnerRequired is returned when no VideoRunner (Veo adapter) was
	// injected into a VideoTimelineRunner. Where ErrVideoRunnerNotConfigured means
	// "called workflows that were built without one", this one means the
	// construction itself was wrong.
	ErrVideoRunnerRequired = errors.New("video runner is required")

	// ErrWriterRequired is returned when no Writer for saving the outputs was injected.
	ErrWriterRequired = errors.New("writer is required")

	// ErrConfigInvalid means Config.Validate found a required field missing (the
	// two model names, and the art-direction KeyframeAspectRatio /
	// KeyframeImageSize; see the Config comments for why). It is a construction
	// mistake rather than a run-time failure, so it is returned at workflow.New.
	ErrConfigInvalid = errors.New("config is invalid")
)
