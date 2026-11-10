# ⚙️ Configuration and DI

[← README](../README.md)

The settings and dependencies passed to `workflow.New(ManagerArgs)`.

## ports.Config

**Four fields are required** (missing ones yield `ports.ErrConfigInvalid`): the two model names and the two art-direction values (aspect ratio and resolution). See "No art-direction defaults" below for why. Execution controls (concurrency, timeout) may be left at their zero values (`ApplyDefaults` fills them in). `RateInterval` alone is not filled in — 0 is a meaningful value there, "no limit".

| Field | Role |
| --- | --- |
| `GeminiModel` | Text model used for script generation (**required**) |
| `ImageModel` | Model used for keyframe image generation (**required**) |
| `MaxConcurrency` | Maximum concurrency of keyframe generation (video generation is always sequential, because of the Video-to-Video chain) |
| `RateInterval` | Minimum interval between AI calls (0 means unlimited). Also applies to the script's text generation |
| `RequestTimeout` | Upper bound on the time of a single AI call (default 5 minutes) |
| `KeyframeAspectRatio` | Keyframe aspect ratio (e.g. `16:9`, `9:16`). **Required** |
| `KeyframeImageSize` | Keyframe output resolution (e.g. `1K`, `2K`). **Required** |
| `KeyframeNegativePrompt` | Elements to exclude from keyframes (e.g. `speech bubble, text, watermark`). Optional (empty means "exclude nothing") |

`Config.WithModels(gemini, image)` / `Config.WithAspectRatio(ratio)` return a copy with some values overridden (an empty string means "unchanged").

## ManagerArgs

| Field | Role |
| --- | --- |
| `AIClient` | `gemini.Generator` (script generation and keyframe generation). Only the one generation method is required |
| `Reader` | `ports.ContentReader`. Fetches the script source (reference images travel as `gs://` URIs, so it is not used for images) |
| `Writer` | `remoteio.Writer`. **Must be safe for concurrent use** — each cut's goroutine saves its keyframe right after generating it |
| `VideoRunner` | The Veo API adapter. When unset, a dummy that returns `ErrVideoRunnerNotConfigured` is used |
| `PromptDeps` | Character definitions and prompt implementations (below) |

## PromptDeps

Prompts are not part of this library; they are injected through `PromptDeps`.

| Field | Interface | Role |
| --- | --- | --- |
| `Characters` | `*characterkit.Characters` | Character definitions. Resolved from each cut's `CharacterID`; an unknown ID falls back to the default character |
| `ScriptPrompt` | `ports.ScriptPrompt` | The prompt that generates a video script from a Music Recipe (`Build(mode, *TemplateData)`) |
| `KeyframePrompt` | `ports.KeyframePrompt` | Prompts for generating and editing a cut's keyframe (`BuildCut` / `BuildEdit`) |

## Internal keyframe generation options

`keyframe` and `runner` are in `internal/`, so consumers never touch them directly. `workflow.New` assembles them from `Config` as follows.

| Config | How it is used internally |
| --- | --- |
| `KeyframeAspectRatio` | Aspect ratio (a character reference image matching it is preferred when one exists) |
| `KeyframeImageSize` | Resolution |
| `KeyframeNegativePrompt` | Negative prompt (when unset, nothing is excluded) |
| `MaxConcurrency` | Concurrency across cuts. Applied by `runner.CutKeyframeRunner` with `errgroup` |
| `RateInterval` / `RequestTimeout` | `workflow`'s `callGuard` |

`RateInterval` and `RequestTimeout` apply to **both the script's text generation and the keyframes' image generation** (quota is per project, not per kind of operation). The wait for the call interval happens outside the per-call time limit, so congestion never turns into a timeout.

Concurrency lives in `runner` rather than `keyframe` because **`runner` also owns saving**. Completing one cut's "generate → save" in a single goroutine means whatever was saved survives a crash midway. Note that when `RateInterval` is set, throughput is capped by the call interval regardless of concurrency.

The `Cache-Control` used when saving can be replaced with `runner.WithCacheControl` (default `public, max-age=1800`; a deployment that does not want its outputs public should specify `private` or similar).

Keyframe results are handled as the library's own type `video.KeyframeImage` (`Data` / `MimeType` / `UsedSeed` / `Model` / `Prompt`). It is the boundary that keeps the image kit's types off the public surface, so consumers do not need to import the image kit.

## No art-direction defaults

Making `KeyframeAspectRatio` / `KeyframeImageSize` required, and holding no default wording for `KeyframeNegativePrompt`, is deliberate.

These values change from work to work, and the caller already has its own setting for them (`VEO_ASPECT_RATIO` in ap-mv). If the kit carried defaults as well, there would be two sources, and changing only one would make them disagree silently — in the form of setting `9:16` for a short video and still getting keyframes baked at the kit's `16:9`. Nothing would show up in the logs either.

The negative prompt is kept out of the kit because art style creeps into it. Excluding text and speech bubbles may be universal, but `monochrome` / `black and white` is art direction itself, and a work that wanted to be monochrome could not be made without a kit release.
