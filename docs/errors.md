# ⚠️ Sentinel Errors

[← README](../README.md)

The following sentinel errors are exported so that callers can check them with `errors.Is` and take a different course (fallback or retry) than for a generic error. All but one live in the `ports` package (`video.ErrRecipeInvalid` lives in `video`, because `video.Recipe.Validate` returns it and `video`, the bottom layer, cannot import `ports`).

| Error | When it occurs | Expected caller response |
| --- | --- | --- |
| `ports.ErrConfigInvalid` | `Config.Validate` found one of the four required fields (`GeminiModel` / `ImageModel` / `KeyframeAspectRatio` / `KeyframeImageSize`) missing. Returned at `workflow.New` | A construction (DI) mistake. Report the misconfiguration |
| `ports.ErrRecipeRequired` | `nil` was passed to an operation that requires a `VideoRecipe` | A bug in the caller. Should not normally happen |
| `ports.ErrEditingNotSupported` | In `EditAndSave`, the configured image generator does not implement keyframe editing (`EditCut`) | Fall back to full regeneration (`GenerateAndSave`) |
| `ports.ErrInvalidAIResponse` | The AI's response text could not be parsed as `VideoRecipe` JSON | Decide on retry separately from network/auth errors |
| `ports.ErrVideoRunnerNotConfigured` | `Workflows.Video` was called without a `VideoRunner` configured | Skip the video generation step, report the misconfiguration |
| `ports.ErrInputTooLarge` | The source input exceeded the size limit (5MB) | Ask for the input to be split or the source revised |
| `ports.ErrUnsupportedCutDuration` | A cut's duration is not accepted by the resolved Veo generation mode | A duration-planning mistake on the recipe side. Do not retry; reassign durations with `veo.SnapDuration` etc. |
| `ports.ErrNoKeyframeToEdit` | The cut targeted by `EditAndSave` has no existing keyframe | Generate the keyframe with `GenerateAndSave` first |
| `video.ErrRecipeInvalid` | `VideoRecipe.Validate` found a structural problem (missing title, no cuts, out-of-range `section_index`) | Regenerate if AI-generated; correct the input if hand-written |
| `ports.ErrVideoRunnerRequired` / `ErrWriterRequired` | Run without the Runner or Writer injected | A construction (DI) mistake. Report the misconfiguration |
