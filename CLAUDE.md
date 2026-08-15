# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A Go library (not a standalone service) that orchestrates Music-Recipe-driven video generation for Google's Veo API. It turns a Lyria `MusicRecipe` into a `VideoRecipe` (a sequence of `Cut`s), generates character-consistent keyframe images for each cut, then feeds `Prompt + Keyframe + Audio + PreviousVideoURI + Seed` to Veo one cut at a time, chaining `video_id` from one cut into the next (`PreviousVideoURI`) for Video-to-Video continuity. That field is a **`gs://` URI contract**: a value that is not `gs://`-prefixed never classifies as video_extension, so an adapter that puts an operation name or a signed URL there silently downgrades the whole chain. This repo contains no Veo client implementation — the actual Veo/Vertex AI call is injected by the caller via `ports.VideoRunner`. README.md is the entry point (overview, quick start, doc index); the full domain writeup (Japanese) lives in `docs/` — `music-recipe.md` (input schema), `configuration.md`, `adapter.md`, `veo-modes.md`, `recipe-api.md`, `errors.md`, `architecture.md` (package layout, persistence split, sequence diagrams). Read the relevant page before making non-trivial changes, and keep it in sync when changing a contract.

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
keyframe/  Generator (per-cut keyframe image generation, parallel across cuts). It holds
           the character definitions directly and passes each cut's gs:// reference URL
           straight through — Vertex AI resolves it, so nothing is fetched or uploaded.
           Concurrency lives here; rate interval and per-call timeout live in workflow.
runner/    Concrete Runner implementations: VideoScriptRunner, CutKeyframeRunner,
           VideoTimelineRunner (+ VideoRequestBuilder), VideoPublisherRunner.
workflow/  manager: workflow.New(ManagerArgs) builds the generationUnit (image core +
           generator) and all four Runners, returning *ports.Workflows. This is the one
           package an external caller imports to construct the library; ManagerArgs.VideoRunner
           is the injection point for the real Veo adapter.
```

### The Runner/Workflow contract

`ports.Workflows` exposes four runners, each independently usable: `Script` (Music Recipe → `VideoRecipe`), `CutKeyframe` (per-cut keyframe image generation, plus `EditAndSave` for localized edits to an existing keyframe), `Video` (`VideoTimelineRunner`: sequential Video-to-Video chain generation), `Publish` (writes `video_music_meta.json`).

Persisting results is split deliberately. `CutKeyframeRunner` writes its own artifacts (`RunAndSave`, `EditAndSave`) because the filenames encode each cut's position in the recipe — a caller doing that itself would have to reimplement the naming and set `KeyframeReference` without owning the recipe's invariants. `VideoTimelineRunner` does **not** save: it only generates, and the caller invokes `Publish` when it is ready. It used to have a `RunAndSave`, but publishing immediately after generation is wrong for any caller that needs a step in between — ap-mv concatenates the video chains and sets `FinalVideoURL` first, so a metadata file written by the timeline runner would always be missing that field. Nothing called it. Keep this split: generation and publication are separate calls on the video side.

If `ManagerArgs.VideoRunner` is nil, `Workflows.Video` is **not nil** — it's `ports.NewNoopVideoTimelineRunner()`, which always returns `ports.ErrVideoRunnerNotConfigured`. Check with `errors.Is(err, ports.ErrVideoRunnerNotConfigured)`, never `Workflows.Video == nil`.

### VideoRecipe / Cut model (`ports/recipe.go`)

`Cut` is JSON-flat but Go-composed: fields are grouped into embedded structs `AudioSync`, `KeyframeResult`, `VideoResult`, `ChainControl`. Field access (`cut.VideoID`, `cut.DurationSec`) is unaffected, but composite literals must nest by group — `ports.Cut{AudioSync: ports.AudioSync{DurationSec: 5}, KeyframeResult: ports.KeyframeResult{KeyframeReference: "..."}}`, not a flat literal.

`VideoRecipe.Normalize()` (call before processing any recipe that might come from partially-hand-authored JSON) fills in cut numbering, `start_sec`/`end_sec` from cumulative `duration_sec`, default `status: "pending"`, generates `Cuts` from `MusicRecipe.Sections` if `Cuts` is empty, and propagates `LocationAnchor` from the recipe down to every cut (keyframe prompt builders only ever see one `Cut`, never the parent recipe, so this is how a tight close-up cut still knows the persistent scene setting).

A cut is skippable/resumable when `Cut.IsGenerated()` — `status == "generated"` or both `video_id` and `video_url` are set. `VideoTimelineRunner.Run` uses this to avoid regenerating already-completed cuts and to keep the `PreviousVideoURI` chain intact across a resumed run.

Keyframes have the same rule under a different field: **both** `CutKeyframeRunner.Run` and `RunAndSave` generate only the cuts whose `KeyframeReference` is empty. `Run` returns a slice aligned position-for-position with `recipe.Cuts`, holding `nil` where a cut already had an image — `VideoTimelineRunner.prepareKeyframes` and `RunAndSave` both read `nil` as "reuse the cut's existing reference", so neither pays to re-bake. `RunAndSave` writes the metadata either way (a caller that finds jobs by `video_music_meta.json` must still see the job when nothing was generated). The two conditions are deliberately separate — a cut can have its keyframe baked while its video is still pending, which is the normal state between the keyframe stage and video generation. **Clearing `KeyframeReference` is how a caller asks for a re-bake**; there is no "force" flag, because a flag would let a caller regenerate images while still claiming the recipe describes them. Generation is driven off a `[]Cut` subset rather than a sub-`VideoRecipe`, since `VideoRecipe.Normalize()` renumbers `CutIndex` from 1 and would desync a partial batch from the parent recipe; saved filenames use the cut's position in the parent recipe so a partial batch never writes `keyframe_1.png` for cut 5.

`SectionIndex` on a `Cut` is the 1-based position in `MusicRecipe.Sections` it was derived from; when one section splits into multiple cuts (scene_split), all resulting cuts keep the same `SectionIndex`, so callers can determine section membership directly instead of reverse-matching `StartSec` against section time ranges.

### Veo request classification and cut durations (`ports/veo_mode.go`, `ports/veo_duration.go`)

Which Veo feature a request resolves to (`video_extension` / `reference_to_video` / `frames_to_video` / `image_to_video`) is decided in exactly one place: `ports.ClassifyVeoRequest(req, usePreviousVideo, caps)`. Adapter request-body construction, cut-duration planning, and generation-mode-specific prompt selection all consume that one decision — when adding a new Veo input mode, extend the classifier and its capability struct `ports.VeoCapabilities` rather than adding branches at call sites. Model capabilities come from optional interfaces on the `VideoRunner` (`ReferenceImagesSupporter`, `LastFrameSupporter`) via `ports.RunnerCapabilities`; a runner implementing neither reports no optional support and falls back to `image_to_video`.

Veo accepts only discrete cut durations, and which set applies depends on the resolved mode: `reference_to_video` is 8s only, `video_extension` is 7s only, the image-input modes take {4,6,8}. `ports.DurationsForMode` / `IsSupportedDuration` / `SnapDuration` / `ChainDurations` are the shared primitives — do not re-derive these numbers at call sites. Duration *planning* (splitting a long cut, error-diffusing rounding across a timeline) is deliberately the caller's job; the library only supplies the rules and rejects violations. `VideoTimelineRunner.Run` validates each cut against its resolved mode before calling Veo and returns `ports.ErrUnsupportedCutDuration` rather than paying for a long-running operation that Veo will reject.

`ports.CutReferenceImages(cut, characters)` is the single rule for building `referenceImages` (`[character art, keyframe]`, max 3, blanks skipped). `DefaultVideoRequestBuilder` and any caller classifying a cut must both use it, so the duration a cut is rounded to always matches the mode its request actually resolves to.

`DefaultVideoRequestBuilder.Build` takes a `runner.BuildInput` struct (not positional args) and prunes inputs the resolved mode won't use — under `video_extension` it drops all image inputs, under non-`frames_to_video` modes it drops `LastFrameReference`. The built request therefore matches what the adapter sends, so logs never show inputs that were silently ignored.

### Adapter boundary (`ports.VideoRunner`)

The real Veo/Vertex AI call is entirely external to this repo. An adapter's `Run(ctx, VideoGenerationRequest) (*VideoResponse, error)` is responsible for auth, resolving `ImageReference`/`AudioReference` (preferred) vs. uploading `InputImage`/`InputAudio` (fallback when the reference is empty), submitting to Veo, polling long-running operations, and returning `VideoID` as a `gs://` URI (needed to chain into the next cut's `PreviousVideoURI`) and `CloudURL`. `ReferenceImages` (max 3, `characterkit` character art + keyframe) takes priority over `ImageReference`/`InputImage` when both are supplied by `DefaultVideoRequestBuilder`. `LastFrameReference` is only meaningful for image-to-video Veo 2 / Veo 3.1 requests and must be paired with a start frame.

### Sentinel errors (`ports/errors.go`)

Callers use `errors.Is` against these to branch on specific failure modes rather than treating all errors alike: `ErrRecipeRequired`, `ErrEditingNotSupported` (image generator doesn't implement `EditCut`; caller can fall back to full `RunAndSave` regeneration), `ErrInvalidAIResponse` (AI text didn't parse as VideoRecipe JSON — distinguish from network/auth errors when deciding to retry), `ErrVideoRunnerNotConfigured`, `ErrInputTooLarge`, `ErrUnsupportedCutDuration` (recipe-side duration planning bug — retrying will never fix it), `ErrNoKeyframeToEdit`, `ErrRecipeInvalid`, the DI-failure trio `ErrKeyframeRunnerRequired` / `ErrVideoRunnerRequired` / `ErrWriterRequired`, and `ErrConfigInvalid` (`Config.Validate` at `workflow.New`: `GeminiModel` / `ImageModel` are required — this library keeps no default model names, since model IDs rot on Google's release schedule, not this repo's).

### Concurrency notes

Reference-image resolution used to live here as `keyframe.Composer` — a pre-upload pass with double-checked locking and `singleflight`, caching File API uploads. That whole problem is gone: on Vertex AI a `gs://` URI is handed to the model as-is, so there is nothing to fetch, upload or cache. **Do not reintroduce a pre-upload pass or a reference cache** — if one ever looks necessary, the real cause is a reference that stopped being `gs://`, and that is the thing to fix. **One reference image per cut is deliberate.** `Cut.CharacterID` is singular and `keyframe.Generator` attaches exactly one reference (the aspect-ratio-matched variant when the character has one), because multi-subject image generation is not reliable enough at the current model generation to be worth the schema change. The switch point when that changes is now just data: `ImageGenerator.Generate(ImageRequest)` takes `ImageRequest.Images` holding 0..n references, so attaching a second reference needs no API change at all. The likely first use is not multiple characters but **continuity**: attaching the previous cut's keyframe alongside the character reference for cuts that continue a chain (`ChainControl.IsChainStart` is false), the same trick go-comic-kit uses when composing a page from its panels.

**AI-call guards are split across two layers on purpose.** `workflow` owns the rate interval and the per-call timeout (`callGuard` in `singleflight.go`, fed by `Config.RateInterval` / `Config.RequestTimeout`) and applies them to **both** script text generation and keyframe image generation — Gemini quota is per project, not per operation kind, so guarding only the image path leaves the text path free to blow the same quota. The rate-limit wait happens *outside* the timeout, so a busy queue cannot manufacture timeouts. Those guards are deliberately **not** delegated to vertex-image-kit's `WithRateLimit` / `WithRequestTimeout`, which would only cover images. `keyframe.Generator` owns the concurrency limit instead (`Config.MaxConcurrency` → `keyframe.WithMaxConcurrency`, applied with `errgroup.SetLimit`), because delegating to the kit's `GenerateBatch` would remove the per-image hook that emits progress logs — with a single keyframe taking minutes, "3/12, 47s" is how a run is monitored. Note that with a non-zero `RateInterval`, throughput is capped at `1/RateInterval` regardless of concurrency; setting both aggressively is contradictory.

Both AI paths are also wrapped in **singleflight decorators** (`workflow/singleflight.go`): identical in-flight requests collapse into one API call, shared responses are cloned per caller, and the shared execution runs on a detached context so one caller's cancel cannot kill the callers piggybacking on it. This dedupes within one process only — durable idempotency comes from the recipe's `keyframe_reference` / `video_id`. This mirrors go-comic-kit, which is where the pattern comes from.

A cut that fails does not discard the images already paid for: partial successes are kept and the failures are aggregated with `errors.Join`. `VideoTimelineRunner.Run`, by contrast, is strictly sequential per cut — the Video-to-Video chain requires each cut's `PreviousVideoURI` before the next can start.

## External dependencies worth knowing

- `github.com/shouni/vertex-image-kit` — keyframe/still-image generation on Vertex AI (`generator.New` → `*generator.Generator`); this repo's `imagePorts.ImageGenerator` / `ImageRequest` / `ImageResponse` / `ImageURI` come from its `ports` package. It accepts **`gs://` references only** and transfers no bytes, which is why this repo needs no GCS reader or HTTP client for images. It replaced `gemini-image-kit`, whose File API upload, cache and fetch layers never executed on the Vertex backend.
- `github.com/shouni/go-character-kit` — `characterkit.Characters`/`Character` (Seed, ReferenceURL(s)) for cross-cut character consistency.
- `github.com/shouni/go-gemini-client` — `music.Recipe` (aliased as `ports.MusicRecipe`) is the input music/lyrics recipe format — it lives in the leaf `music` package, so importing it does not drag in the `lyria` workflow. AI clients are taken as `gemini.Generator` (script generation) and `gemini.Model` (image core), the genai-free interfaces: nothing in this repo imports `google.golang.org/genai`. Structured output uses plain JSON Schema via `GenerateOptions.ResponseJSONSchema` (`ports.VideoRecipeSchema` returns `map[string]any`), not `genai.Schema`.
- `github.com/shouni/go-remote-io` — `remoteio.Writer` used for persisting `video_music_meta.json` and keyframe outputs.
