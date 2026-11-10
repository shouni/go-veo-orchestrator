# 🔌 Adapter Boundary — implementing VideoRunner

[← README](../README.md)

This repository is not a Veo API client. It is an orchestration library responsible for what is handed to Veo: **the domain model, keyframe generation, request building, the Video-to-Video chain and metadata persistence**.

Actual communication with the Veo API is plugged in as an implementation of `ports.VideoRunner`, from the consuming application or a separate package. No production Veo adapter lives in this repository; differences between runtime environments stay confined to the adapter.

## What the adapter is responsible for

* Authentication with Google Cloud / Vertex AI / Gemini API, etc.
* Resolving inputs (prefer the reference URI, upload bytes only when it is empty — see the table below)
* Sending the video generation request to the Veo API
* Polling the long-running operation, timeouts and retries
* Managing where generated videos are stored
* Returning `video.Response.VideoID` so the next cut can chain from it
* Returning a reachable `video.Response.CloudURL`

## Example implementation

```go
type VeoRunner struct {
	// Holds what the runtime environment needs: client, bucket, model, location, etc.
}

func (r *VeoRunner) Run(ctx context.Context, req video.GenerationRequest) (*video.Response, error) {
	// 1. Use req.ReferenceImages as the image input if present, otherwise req.ImageReference
	// 2. Only when the references are empty, upload req.InputImage / req.InputAudio to get a URI
	// 3. Pass req.Prompt, req.PreviousVideoURI, req.Seed and req.DurationSec to the Veo API
	// 4. Poll the operation until it completes
	// 5. Return CloudURL and VideoID
	return &video.Response{
		CloudURL:    "gs://example-bucket/videos/cut_001.mp4",
		VideoID:     "veo-video-id",
		CutIndex:    req.CutIndex,
		DurationSec: req.DurationSec,
		MimeType:    "video/mp4",
	}, nil
}
```

## The video.GenerationRequest contract

| Field | How the adapter treats it |
| --- | --- |
| `Prompt` | The final prompt for Veo. Includes the cut's content, camera work and music-sync directions. |
| `ReferenceImages` | `referenceImages` (asset type, up to 3, `[character art, keyframe]`). **When set, it takes priority over `ImageReference` / `InputImage`.** |
| `ImageReference` | An already-reachable keyframe image URI. When present, it takes priority over `InputImage`. |
| `InputImage` | Image bytes the adapter uploads and uses when `ImageReference` is empty. |
| `AudioReference` | An already-reachable audio segment URI. |
| `InputAudio` | Audio bytes the adapter uploads and uses when `AudioReference` is empty. |
| `PreviousVideoURI` | The `gs://` URI of the video whose context this cut continues (see the note below). When empty, the cut is generated without a chain. |
| `LastFrameReference` | Image URI used as the end frame (Veo first/last frame interpolation). The Veo API requires it to be paired with a start frame image, so it is sent as `lastFrame` only for image input (image_to_video). Supported only by Veo 2 / Veo 3.1 models. |
| `Seed` | The character Seed takes priority; when unset, `music_recipe.Seed` is used. |
| `CutIndex` | The cut number, used in responses and error messages. |
| `DurationSec` | The cut's target duration in seconds. |

When `video.Response.VideoID` is empty, the cut's result can still be saved, but the `PreviousVideoURI` chain into the next cut is not updated. An adapter that cares about continuity across cuts should return Veo's video ID whenever it can.

> **`PreviousVideoURI` is a `gs://` URI contract.** A value that does not start with `gs://` is never classified as video_extension, so an adapter that puts an operation name or a signed URL there silently downgrades the whole chain to image_to_video.

## Replacing the keyframe generator

Keyframe generation itself is `ports.CutImageGenerator` (one method, `GenerateCut(ctx, cut, index, total)`). The `EditAndSave` editing path is separate: if the injected generator also has `EditCut(ctx, cut, editPrompt)`, it is detected at run time and used; otherwise `ports.ErrEditingNotSupported` is returned.

## Without a VideoRunner

`Workflows.Video` returned by `workflow.New` is not `nil`: it is a dummy implementation (`ports.NewNoopVideoTimelineRunner()`) that always returns `ports.ErrVideoRunnerNotConfigured` when called. A setup that uses only Script / CutKeyframe / Publish works as-is.

To detect the missing runner, check `errors.Is(err, ports.ErrVideoRunnerNotConfigured)` (a `Workflows.Video == nil` check does not work).

## Declaring what the model supports

Which Veo features are available is picked up automatically by `veo.RunnerCapabilities` when the `VideoRunner` implements the following optional interfaces. A Runner implementing neither reports both as false and falls back to image_to_video.

```go
type ReferenceImagesSupporter interface{ SupportsReferenceImages() bool }
type LastFrameSupporter      interface{ SupportsLastFrame() bool }
```

For the details of classification, see [Veo generation modes and cut durations](veo-modes.md).
