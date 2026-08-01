# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A Go library (not a standalone service) that orchestrates Music-Recipe-driven video generation for Google's Veo API. It turns a Lyria `MusicRecipe` into a `VideoRecipe` (a sequence of `Cut`s), generates character-consistent keyframe images for each cut, then feeds `Prompt + Keyframe + Audio + PreviousVideoID + Seed` to Veo one cut at a time, chaining `video_id` from one cut into the next (`PreviousVideoID`) for Video-to-Video continuity. This repo contains no Veo client implementation — the actual Veo/Vertex AI call is injected by the caller via `ports.VideoRunner`. See README.md for the full domain writeup (Japanese), Music Recipe JSON schema, and sequence diagrams — read it before making non-trivial changes here.

## Commands

```bash
go build ./...                   # build everything
go vet ./...
gofmt -l .                       # must print nothing (CI enforces this, not `gofmt -w` output)
go test -race ./...              # full test suite, as run in CI
go test -race ./ports/...        # single package
go test -race -run TestName ./runner/...   # single test
golangci-lint run                # matches .golangci.yml (errcheck, govet, ineffassign, staticcheck, unused, gocritic, revive)
```

CI (`.github/workflows/ci.yml`) runs three parallel jobs on push/PR to `main`/`develop`: build+vet+gofmt+`go test -race`, `golangci-lint`, and `govulncheck`. Match these locally before pushing.

Go version is pinned in `go.mod` (currently 1.26) — `setup-go` reads it from there, so bump `go.mod` rather than hardcoding a version elsewhere.

## Architecture

Four packages, strict dependency direction: `ports` is the contract layer everything else depends on; `keyframe` and `runner` implement pieces against those contracts; `workflow` wires concrete implementations together into the public `ports.Workflows` struct. Never import `runner` or `keyframe` from `ports`.

```
ports/     Interfaces (VideoRunner, ScriptPrompt, KeyframePrompt, ...), domain models
           (VideoRecipe, Cut, VideoGenerationRequest), Veo request classification and
           duration rules (veo_mode.go, veo_duration.go), Config, sentinel errors. Everything
           else depends on this package; it depends on nothing else in-repo.
keyframe/  Generator (concurrent, rate-limited keyframe image generation per cut). It holds
           the character definitions directly and passes each cut's reference URL through;
           resolving that URL (Vertex + gs:// passes through, Gemini API uploads once to the
           File API) is gemini-image-kit's job, not this package's.
runner/    Concrete Runner implementations: VideoScriptRunner, CutKeyframeRunner,
           VideoTimelineRunner (+ VideoRequestBuilder), VideoPublisherRunner.
workflow/  manager: workflow.New(ManagerArgs) builds the generationUnit (image core +
           generator) and all four Runners, returning *ports.Workflows. This is the one
           package an external caller imports to construct the library; ManagerArgs.VideoRunner
           is the injection point for the real Veo adapter.
```

### The Runner/Workflow contract

`ports.Workflows` exposes four runners, each independently usable: `Script` (Music Recipe → `VideoRecipe`), `CutKeyframe` (per-cut keyframe image generation, plus `EditAndSave` for localized edits to an existing keyframe), `Video` (`VideoTimelineRunner`: sequential Video-to-Video chain generation), `Publish` (writes `video_music_meta.json`).

If `ManagerArgs.VideoRunner` is nil, `Workflows.Video` is **not nil** — it's `ports.NewNoopVideoTimelineRunner()`, which always returns `ports.ErrVideoRunnerNotConfigured`. Check with `errors.Is(err, ports.ErrVideoRunnerNotConfigured)`, never `Workflows.Video == nil`.

### VideoRecipe / Cut model (`ports/recipe.go`)

`Cut` is JSON-flat but Go-composed: fields are grouped into embedded structs `AudioSync`, `KeyframeResult`, `VideoResult`, `ChainControl`. Field access (`cut.VideoID`, `cut.DurationSec`) is unaffected, but composite literals must nest by group — `ports.Cut{AudioSync: ports.AudioSync{DurationSec: 5}, KeyframeResult: ports.KeyframeResult{KeyframeReference: "..."}}`, not a flat literal.

`VideoRecipe.Normalize()` (call before processing any recipe that might come from partially-hand-authored JSON) fills in cut numbering, `start_sec`/`end_sec` from cumulative `duration_sec`, default `status: "pending"`, generates `Cuts` from `MusicRecipe.Sections` if `Cuts` is empty, and propagates `LocationAnchor` from the recipe down to every cut (keyframe prompt builders only ever see one `Cut`, never the parent recipe, so this is how a tight close-up cut still knows the persistent scene setting).

A cut is skippable/resumable when `Cut.IsGenerated()` — `status == "generated"` or both `video_id` and `video_url` are set. `VideoTimelineRunner.Run` uses this to avoid regenerating already-completed cuts and to keep the `PreviousVideoID` chain intact across a resumed run.

`SectionIndex` on a `Cut` is the 1-based position in `MusicRecipe.Sections` it was derived from; when one section splits into multiple cuts (scene_split), all resulting cuts keep the same `SectionIndex`, so callers can determine section membership directly instead of reverse-matching `StartSec` against section time ranges.

### Veo request classification and cut durations (`ports/veo_mode.go`, `ports/veo_duration.go`)

Which Veo feature a request resolves to (`video_extension` / `reference_to_video` / `frames_to_video` / `image_to_video`) is decided in exactly one place: `ports.ClassifyVeoRequest(req, usePreviousVideo, caps)`. Adapter request-body construction, cut-duration planning, and generation-mode-specific prompt selection all consume that one decision — when adding a new Veo input mode, extend the classifier and its capability struct `ports.VeoCapabilities` rather than adding branches at call sites. Model capabilities come from optional interfaces on the `VideoRunner` (`ReferenceImagesSupporter`, `LastFrameSupporter`) via `ports.RunnerCapabilities`; a runner implementing neither reports no optional support and falls back to `image_to_video`.

Veo accepts only discrete cut durations, and which set applies depends on the resolved mode: `reference_to_video` is 8s only, `video_extension` is 7s only, the image-input modes take {4,6,8}. `ports.DurationsForMode` / `IsSupportedDuration` / `SnapDuration` / `ChainDurations` are the shared primitives — do not re-derive these numbers at call sites. Duration *planning* (splitting a long cut, error-diffusing rounding across a timeline) is deliberately the caller's job; the library only supplies the rules and rejects violations. `VideoTimelineRunner.Run` validates each cut against its resolved mode before calling Veo and returns `ports.ErrUnsupportedCutDuration` rather than paying for a long-running operation that Veo will reject.

`ports.CutReferenceImages(cut, characters)` is the single rule for building `referenceImages` (`[character art, keyframe]`, max 3, blanks skipped). `DefaultVideoRequestBuilder` and any caller classifying a cut must both use it, so the duration a cut is rounded to always matches the mode its request actually resolves to.

`DefaultVideoRequestBuilder.Build` takes a `runner.BuildInput` struct (not positional args) and prunes inputs the resolved mode won't use — under `video_extension` it drops all image inputs, under non-`frames_to_video` modes it drops `LastFrameReference`. The built request therefore matches what the adapter sends, so logs never show inputs that were silently ignored.

### Adapter boundary (`ports.VideoRunner`)

The real Veo/Vertex AI call is entirely external to this repo. An adapter's `Run(ctx, VideoGenerationRequest) (*VideoResponse, error)` is responsible for auth, resolving `ImageReference`/`AudioReference` (preferred) vs. uploading `InputImage`/`InputAudio` (fallback when the reference is empty), submitting to Veo, polling long-running operations, and returning `VideoID` (needed to chain into the next cut's `PreviousVideoID`) and `CloudURL`. `ReferenceImages` (max 3, `characterkit` character art + keyframe) takes priority over `ImageReference`/`InputImage` when both are supplied by `DefaultVideoRequestBuilder`. `LastFrameReference` is only meaningful for image-to-video Veo 2 / Veo 3.1 requests and must be paired with a start frame.

### Sentinel errors (`ports/errors.go`)

Callers use `errors.Is` against these to branch on specific failure modes rather than treating all errors alike: `ErrRecipeRequired`, `ErrEditingNotSupported` (image generator doesn't implement `EditCut`; caller can fall back to full `RunAndSave` regeneration), `ErrInvalidAIResponse` (AI text didn't parse as VideoRecipe JSON — distinguish from network/auth errors when deciding to retry), `ErrVideoRunnerNotConfigured`, `ErrInputTooLarge`, `ErrUnsupportedCutDuration` (recipe-side duration planning bug — retrying will never fix it), `ErrNoKeyframeToEdit`, `ErrSingleCutRequired`.

### Concurrency notes

Reference-image resolution used to live here as `keyframe.Composer` — a pre-upload pass with double-checked locking and `singleflight` on top of `AssetManager`. gemini-image-kit v1.12.2 moved that decision into `ports.ReferenceResolver`, with its own cache and singleflight, so the Composer was deleted rather than kept as a second cache in front of it. **Do not reintroduce a pre-upload pass**: this copy had already drifted from go-comic-kit's (it was missing both the re-check inside the write lock and the `DoChan` + `WithoutCancel` fix that keeps one caller's cancel from killing piggybacked uploads), which is exactly what duplicating the logic costs. **One reference image per cut is deliberate.** `Cut.CharacterID` is singular and `keyframe.Generator` attaches exactly one reference (the aspect-ratio-matched variant when the character has one), because multi-subject image generation is not reliable enough at the current model generation to be worth the schema change. The switch point when that changes is small and known: `keyframe.ImageGenerator` moves from `GenerateSingleImage`/`SingleImageRequest` to `GenerateFusedImage`/`ImageFusionRequest` — gemini-image-kit already supports both. The likely first use is not multiple characters but **continuity**: attaching the previous cut's keyframe alongside the character reference for cuts that continue a chain (`ChainControl.IsChainStart` is false), the same trick go-comic-kit uses when composing a page from its panels.

`keyframe.Generator` runs cut keyframe generation concurrently with a configurable `MaxConcurrency` and `RateInterval` (see `keyframe.WithMaxConcurrency`, `keyframe.WithRateInterval` in `workflow/runners.go`). `VideoTimelineRunner.Run`, by contrast, is strictly sequential per cut — the Video-to-Video chain requires each cut's `PreviousVideoID` before the next can start.

## External dependencies worth knowing

- `github.com/shouni/gemini-image-kit` — keyframe/still-image generation core (`generator.GeminiImageCore`, `generator.GeminiGenerator`); this repo's `imagePorts.ImageGenerator`/`AssetManager`/`Backend` types come from here.
- `github.com/shouni/go-character-kit` — `characterkit.Characters`/`Character` (Seed, ReferenceURL(s)) for cross-cut character consistency.
- `github.com/shouni/go-gemini-client` — `lyria.MusicRecipe` (aliased as `ports.MusicRecipe`) is the input music/lyrics recipe format. AI clients are taken as `gemini.MultimodalGenerator` (script generation) and `gemini.MultimodalModel` (image core), the genai-free interfaces: nothing in this repo imports `google.golang.org/genai`. Structured output uses plain JSON Schema via `GenerateOptions.ResponseJSONSchema` (`ports.VideoRecipeSchema` returns `map[string]any`), not `genai.Schema`.
- `github.com/shouni/go-remote-io` — `remoteio.Writer` used for persisting `video_music_meta.json` and keyframe outputs.
