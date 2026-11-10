# 🔧 Working with recipes and cuts

[← README](../README.md)

`VideoRecipe` / `Cut` have helpers that read and write state for resuming and regenerating. Code that resumes a batch generation midway uses them to decide which cuts are still to do.

## Helpers

| API | Purpose |
| --- | --- |
| `Cut.IsGenerated()` / `Cut.Status` (`video.CutStatus`) | Whether the cut has been generated, and its state |
| `Cut.ResetGeneration(keepKeyframe)` | Discards the generated result and makes the cut a regeneration target again. `keepKeyframe=false` also clears `keyframe_reference`, so the next `GenerateAndSave` re-bakes it starting from the image |
| `Cut.EffectiveDurationSec()` | The cut's effective duration |
| `VideoRecipe.Normalize()` | Fixes up cut numbering, start seconds and `SectionIndex`, and propagates `LocationAnchor` / `AspectRatio` from the recipe to each cut |
| `VideoRecipe.Validate()` | Validates the recipe's consistency |
| `Config.UsesModels(gemini, image)` / `Cuts.UniqueCharacterIDs()` | Whether the models in use match / the set of characters appearing |
| `Cuts.NextLastFrameReference(i)` | Resolves the reference used as the next cut's `lastFrame` in frames_to_video |
| `video.RecipeSchema(characterIDs)` | The structured-output schema for script generation (a JSON Schema constraining `character_id` with an enum) |
| `Cuts.FillAudioReference(url)` / `Cuts.FillCharacterID(id)` | Fills in the audio source / character on every cut that has none |
| `Cuts.IndexOf(cutIndex)` | Looks up the position in the recipe from a cut_index |
| `video.NewRecipeFromMusic(music.Recipe)` | Builds a video recipe from a Music Recipe (deep copy + Normalize) |
| `veo.ExpandCutsToSupportedDurations(...)` | The duration planner that normalizes cut durations to values Veo supports (splitting, chain formation, resets at section boundaries) |
| `veo.CapCutsTotalDuration(cuts, maxSec)` / `veo.ChainTailEnd(cuts, i, usePrev)` | Truncating the total duration / resolving the range of a chain to regenerate |
| `veo.SplitCutBySupportedDurations(cut, allowedDurations)` / `veo.AllowedCutDurations(cut, characters, referenceImagesSupported)` | Splitting one cut, and getting the allowed durations, which depend on whether reference images are present |
| `veo.CutUsesReferenceImages(cut, characters, referenceImagesSupported)` | Whether the cut takes the reference_to_video path (= fixed at 8 seconds) |
| `veo.SplitDialogueLines(dialogue)` / `veo.DistributeLines(lines, pos, total)` | Splitting dialogue into lines, and assigning them to the cuts after a split |
| `veo.ModelCapabilities(model)` | Derives referenceImages / lastFrame support from a model name |

## Partial results and lifecycle

The `*ports.Workflows` returned by `workflow.New` has no `Close()`. Reference images are now passed as `gs://` URIs as-is, so there is no image-cache goroutine left to stop. The output of publishing and saving is `video.PublishResult`.

`VideoTimelineRunner.Run` **returns the partial result of the completed cuts even on error**. `WithCutObserver` lets you insert a hook after each cut completes (saving metadata, checking an execution-time budget); if the hook returns an error, the run stops there and returns the partial result. On a job platform with a time limit, "save for now and resume on the next run" can be built without a cut loop of your own.

Likewise, when generation fails midway, `CutKeyframeRunner.GenerateAndSave` **saves the images and metadata that succeeded before** returning the error.

## 🩹 Editing a single cut's keyframe (EditAndSave)

`CutKeyframeRunner.GenerateAndSave` is a "full generation" that recreates the image from the prompt, whereas `EditAndSave` takes the existing keyframe image as its source and applies only a localized fix described in text. Composition, pose and background are preserved, so it suits minor fixes such as "reduce the number of props" or "match the color tone" while staying consistent with the same character's other cuts.

Pass the whole recipe and the position (0-based) of the target cut within it. The target cut's `KeyframeReference` must point at the existing keyframe image to edit.

```go
// recipe can stay the complete recipe. The cut to edit is given by position.
const cutPosition = 1 // edit recipe.Cuts[1]

updated, err := workflows.CutKeyframe.EditAndSave(ctx, recipe, cutPosition,
	"Keep only one or two bandages on the arm", "gs://bucket/jobs/j1/regens/cut-2/")
if err != nil {
	return err
}
// updated.Cuts[cutPosition].KeyframeReference now points at the edited image.
```

There is no need to build a throwaway recipe holding only the target cut (before v1.11 that was required, and callers looping over cuts rebuilt the recipe each time). The saved metadata is also always the complete recipe.

Internally it calls `imagegen.Generator.Generate` from [genai-kit](https://github.com/shouni/genai-kit) with the existing keyframe image as input and `editPrompt` as the prompt. It reuses the same conversational multimodal image model as `GenerateAndSave` (normal keyframe generation) — `Config.ImageModel`, Gemini's "Nano Banana" family — so no editing-specific model or API is needed.

> Vertex AI Imagen's mask-based editing/customization API (the `imagen-3.0-capability-001` family) was retired on June 30, 2026, and no successor "capability" model exists. `EditAndSave` therefore does not support masks; it supports free-form edit instructions only.

It fails when:

* `cutPosition` is outside the range of `recipe.Cuts`
* the target cut's `KeyframeReference` is empty (= there is no source image): `ports.ErrNoKeyframeToEdit`

As with `GenerateAndSave`, the character's Seed (`char.Seed`) is used as-is for the edit request.
