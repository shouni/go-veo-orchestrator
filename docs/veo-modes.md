# 🎛️ Veo generation modes and cut durations

[← README](../README.md)

Which Veo generation feature a request is interpreted as is decided in **one place**: `veo.ClassifyRequest`. The adapter's request-body construction, cut-duration planning and validation, and per-mode prompt selection must all share this same decision.

When each of them branches on its own, you get drift such as "telling the model to match the reference image while not sending the reference image" or "rounding to 8 seconds assuming reference_to_video when the request was actually image_to_video".

```go
caps := veo.RunnerCapabilities(videoRunner) // derived from the Runner's optional interfaces
mode := veo.ClassifyRequest(req, usePreviousVideo, caps)
```

## Precedence and supported durations

The mode is `veo.GenerationMode`, and the model's support is `veo.Capabilities`. The duration lists are exported as `veo.ImageToVideoDurationsSec` / `veo.ReferenceToVideoDurationsSec`.

| Priority | Mode | Condition | Durations (seconds) |
| --- | --- | --- | --- |
| 1 | `veo.ModeVideoExtension` | `usePreviousVideo` and `PreviousVideoURI` is a `gs://` reference. All image references are ignored | 7 only |
| 2 | `veo.ModeReferenceToVideo` | At least one reference image, and the model supports `referenceImages` (non-Fast Veo 3 models) | 8 only |
| 3 | `veo.ModeFramesToVideo` | Both a start frame and `LastFrameReference`, and the model supports `lastFrame` (Veo 2 / Veo 3.1 models) | 4 / 6 / 8 |
| 4 | `veo.ModeImageToVideo` | Everything else | 4 / 6 / 8 |

For the optional interfaces that declare what a model supports, see [Adapter Boundary](adapter.md#declaring-what-the-model-supports).

## Helpers for planning durations

Veo cannot generate video of arbitrary length, so assign durations on the recipe side to fit these values before running.

| API | Purpose |
| --- | --- |
| `veo.DurationsForMode(mode)` | The durations the mode accepts |
| `veo.IsSupportedDuration(sec, mode)` | Whether a duration is accepted |
| `veo.SnapDuration(sec, candidates)` | Rounds to the nearest supported duration (the longer one on a tie) |
| `veo.ChainDurations(bases)` | Candidate total durations achievable with one continuation chain (base + 7 seconds × n) |
| `veo.ContinuationMaxDurationSec` | The cumulative-duration threshold at which a chain is reset (24 seconds) |

`VideoTimelineRunner.Run` validates each cut's duration right before sending it to Veo, and when it is not supported, returns `ports.ErrUnsupportedCutDuration` without generating that cut. Rather than submitting a long-running operation and waiting for Veo to reject it, it fails up front, stating which cut, how many seconds, and which mode.

## Building the reference images

The rule for building reference images (`referenceImages`) is unified in `video.CutReferenceImages(cut, characters)`. Up to 3 images in the order `[character art, keyframe]`; a cut without character art uses only the keyframe as its reference. The character art uses the variant matching `Cut.AspectRatio` (propagated from `Recipe.AspectRatio` by `Recipe.Normalize`) when one exists, so the keyframe and the character art share the same ratio.

Both the code that classifies a cut and the code that builds its request must go through this function. Otherwise, the duration a cut is rounded to and the mode its request actually resolves to will disagree.
