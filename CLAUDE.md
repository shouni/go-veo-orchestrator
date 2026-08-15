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

Go version is pinned in `go.mod` (currently **1.26.6**) — `setup-go` reads it from there, so bump `go.mod` rather than hardcoding a version elsewhere. The patch level is part of the pin on purpose: `go 1.26` alone let a local 1.26.5 toolchain build the repo with four reachable stdlib CVEs (net/url, crypto/tls, encoding/asn1, net/http), which `govulncheck` reports but no module update can fix. Pinning the patch makes Go fetch a fixed toolchain automatically.

## Architecture

Six packages, strict one-way dependency: `video → ports → veo → internal/{keyframe, runner} → workflow`. `video` depends on nothing in-repo; `workflow` depends on everything. Never import `internal/runner`, `internal/keyframe` or `workflow` from `video`, `ports` or `veo`.

**Four packages are public**: `video`, `ports`, `veo`, `workflow`. `keyframe` and `runner` live under `internal/` because the only consumer (ap-mv) never imported them — it builds everything through `workflow.New` and depends on the `ports` interfaces, so the concrete Runner types were public surface nobody used. Un-hiding one is a one-line move if a second consumer ever needs it.

```
video/     Domain model: Recipe, Cut, Cuts and their methods, GenerationRequest,
           Response, KeyframeImage, PublishResult, the MusicRecipe aliases, and the
           recipe JSON Schema. Leaf package — depends on nothing else in-repo.
ports/     Contracts only: VideoRunner (+ the optional ReferenceImagesSupporter /
           LastFrameSupporter an adapter may implement), ScriptPrompt, KeyframePrompt,
           CutImageGenerator, ContentReader, the four Runner interfaces, Workflows,
           Config, sentinel errors. Imports video; holds no algorithms.
veo/       Veo API constraints: which generation mode a request resolves to
           (ClassifyRequest), which durations each mode allows, and cut planning
           (splitting, capping, chain durations). No API calls happen here.
internal/keyframe/
           Generator: builds and sends the image request for ONE cut. Holds the
           character definitions and passes each cut's gs:// reference URL straight
           through — Vertex AI resolves it, so nothing is fetched or uploaded.
internal/runner/
           Concrete Runner implementations: VideoScriptRunner, CutKeyframeRunner,
           VideoTimelineRunner (+ VideoRequestBuilder), VideoPublisherRunner.
           Owns keyframe concurrency, because it also owns the saving.
workflow/  manager: workflow.New(ManagerArgs) builds the guarded image generator and
           all four Runners, returning *ports.Workflows. This is the one package an
           external caller imports to construct the library; ManagerArgs.VideoRunner
           is the injection point for the real Veo adapter.
```

`ports` used to hold all of the above. It was split because a package named for its contracts should not also be where the domain model and 550 lines of Veo duration algebra live — the same reason go-comic-kit separated `comic` from `ports`. Of the 39 symbols ap-mv imported from the old `ports`, only six were actually contracts.

### The Runner/Workflow contract

`ports.Workflows` exposes four runners, each independently usable: `Script` (Music Recipe → `VideoRecipe`), `CutKeyframe` (per-cut keyframe image generation, plus `EditAndSave` for localized edits to an existing keyframe), `Video` (`VideoTimelineRunner`: sequential Video-to-Video chain generation), `Publish` (writes `video_music_meta.json`).

Persisting results is split deliberately. `CutKeyframeRunner` writes its own artifacts (`GenerateAndSave`, `EditAndSave`) because the filenames encode each cut's position in the recipe — a caller doing that itself would have to reimplement the naming and set `KeyframeReference` without owning the recipe's invariants. `VideoTimelineRunner` does **not** save: it only generates, and the caller invokes `Publish` when it is ready. It used to have a `GenerateAndSave`, but publishing immediately after generation is wrong for any caller that needs a step in between — ap-mv concatenates the video chains and sets `FinalVideoURL` first, so a metadata file written by the timeline runner would always be missing that field. Nothing called it. Keep this split: generation and publication are separate calls on the video side.

If `ManagerArgs.VideoRunner` is nil, `Workflows.Video` is **not nil** — it's `ports.NewNoopVideoTimelineRunner()`, which always returns `ports.ErrVideoRunnerNotConfigured`. Check with `errors.Is(err, ports.ErrVideoRunnerNotConfigured)`, never `Workflows.Video == nil`.

### Recipe / Cut model (`video/recipe.go`)

`Cut` is JSON-flat but Go-composed: fields are grouped into embedded structs `AudioSync`, `KeyframeResult`, `VideoResult`, `ChainControl`. Field access (`cut.VideoID`, `cut.DurationSec`) is unaffected, but composite literals must nest by group — `video.Cut{AudioSync: video.AudioSync{DurationSec: 5}, KeyframeResult: video.KeyframeResult{KeyframeReference: "..."}}`, not a flat literal.

`VideoRecipe.Normalize()` (call before processing any recipe that might come from partially-hand-authored JSON) fills in cut numbering, `start_sec`/`end_sec` from cumulative `duration_sec`, default `status: "pending"`, generates `Cuts` from `MusicRecipe.Sections` if `Cuts` is empty, and propagates `LocationAnchor` from the recipe down to every cut (keyframe prompt builders only ever see one `Cut`, never the parent recipe, so this is how a tight close-up cut still knows the persistent scene setting).

A cut is skippable/resumable when `Cut.IsGenerated()` — `status == "generated"` or both `video_id` and `video_url` are set. `VideoTimelineRunner.Run` uses this to avoid regenerating already-completed cuts and to keep the `PreviousVideoURI` chain intact across a resumed run.

Keyframes follow the same resume rule under a different field: `CutKeyframeRunner.GenerateAndSave` generates only the cuts whose `KeyframeReference` is empty. **It saves each image the moment it is produced** and writes `KeyframeReference` + `KeyframeSeed` onto the cut before moving on, so a crash mid-run (Cloud Run timeout, deploy, OOM) loses at most one image instead of every generated — and already billed — keyframe. Re-running resumes from whatever was persisted. It writes the metadata either way (a caller that finds jobs by `video_music_meta.json` must still see the job when nothing was generated).

Because each cut's generate-and-save runs in its own goroutine, **an injected `remoteio.Writer` must be safe for concurrent use** whenever `Config.MaxConcurrency` is above 1. There is no generate-without-saving path any more, and `VideoTimelineRunner` no longer generates keyframes at all: it consumes `KeyframeReference` and warns about cuts that have none (those become prompt-only generations). The old shape returned in-memory images that were passed to Veo as `InputImage` bytes — that was the flow where a crash threw away paid-for work, and it also let the bytes sent to Veo drift from the file saved to GCS. `KeyframeSeed` exists because the image itself never reaches the recipe, and the video stage reuses the keyframe's seed.

The keyframe stage and the video stage are separate conditions on purpose — a cut can have its keyframe baked while its video is still pending, which is the normal state between the two stages. **Clearing `KeyframeReference` is how a caller asks for a re-bake**; there is no "force" flag, because a flag would let a caller regenerate images while still claiming the recipe describes them. Saved filenames use the cut's position **in the recipe that was passed in**, so a caller generating a subset (ap-mv's per-cut / per-section regeneration builds a temporary recipe holding just the target cuts) must give it its own output prefix, or cut 5 gets written as `keyframe_1.png`.

`SectionIndex` on a `Cut` is the 1-based position in `MusicRecipe.Sections` it was derived from; when one section splits into multiple cuts (scene_split), all resulting cuts keep the same `SectionIndex`, so callers can determine section membership directly instead of reverse-matching `StartSec` against section time ranges.

### Veo request classification and cut durations (`veo/mode.go`, `veo/duration.go`)

Which Veo feature a request resolves to (`video_extension` / `reference_to_video` / `frames_to_video` / `image_to_video`) is decided in exactly one place: `veo.ClassifyRequest(req, usePreviousVideo, caps)`. Adapter request-body construction, cut-duration planning, and generation-mode-specific prompt selection all consume that one decision — when adding a new Veo input mode, extend the classifier and its capability struct `veo.Capabilities` rather than adding branches at call sites. Model capabilities come from optional interfaces on the `VideoRunner` (`ReferenceImagesSupporter`, `LastFrameSupporter`) via `veo.RunnerCapabilities`; a runner implementing neither reports no optional support and falls back to `image_to_video`.

Veo accepts only discrete cut durations, and which set applies depends on the resolved mode: `reference_to_video` is 8s only, `video_extension` is 7s only, the image-input modes take {4,6,8}. `veo.DurationsForMode` / `IsSupportedDuration` / `SnapDuration` / `ChainDurations` are the shared primitives — do not re-derive these numbers at call sites. Duration *planning* (splitting a long cut, error-diffusing rounding across a timeline) is deliberately the caller's job; the library only supplies the rules and rejects violations. `VideoTimelineRunner.Run` validates each cut against its resolved mode before calling Veo and returns `ports.ErrUnsupportedCutDuration` rather than paying for a long-running operation that Veo will reject.

`video.CutReferenceImages(cut, characters)` is the single rule for building `referenceImages` (`[character art, keyframe]`, max 3, blanks skipped). `DefaultVideoRequestBuilder` and any caller classifying a cut must both use it, so the duration a cut is rounded to always matches the mode its request actually resolves to.

`DefaultVideoRequestBuilder.Build` takes a `runner.BuildInput` struct (not positional args) and prunes inputs the resolved mode won't use — under `video_extension` it drops all image inputs, under non-`frames_to_video` modes it drops `LastFrameReference`. The built request therefore matches what the adapter sends, so logs never show inputs that were silently ignored.

### Adapter boundary (`ports.VideoRunner`)

The real Veo/Vertex AI call is entirely external to this repo. An adapter's `Run(ctx, VideoGenerationRequest) (*VideoResponse, error)` is responsible for auth, resolving `ImageReference`/`AudioReference` (preferred) vs. uploading `InputImage`/`InputAudio` (fallback when the reference is empty), submitting to Veo, polling long-running operations, and returning `VideoID` as a `gs://` URI (needed to chain into the next cut's `PreviousVideoURI`) and `CloudURL`. `ReferenceImages` (max 3, `characterkit` character art + keyframe) takes priority over `ImageReference`/`InputImage` when both are supplied by `DefaultVideoRequestBuilder`. `LastFrameReference` is only meaningful for image-to-video Veo 2 / Veo 3.1 requests and must be paired with a start frame.

### Sentinel errors (`ports/errors.go`, `video/errors.go`)

Callers use `errors.Is` against these to branch on specific failure modes rather than treating all errors alike: `ErrRecipeRequired`, `ErrEditingNotSupported` (image generator doesn't implement `EditCut`; caller can fall back to full `GenerateAndSave` regeneration), `ErrInvalidAIResponse` (AI text didn't parse as VideoRecipe JSON — distinguish from network/auth errors when deciding to retry), `ErrVideoRunnerNotConfigured`, `ErrInputTooLarge`, `ErrUnsupportedCutDuration` (recipe-side duration planning bug — retrying will never fix it), `ErrNoKeyframeToEdit`, the DI-failure pair `ErrVideoRunnerRequired` / `ErrWriterRequired`, and `ErrConfigInvalid` (`Config.Validate` at `workflow.New` requires `GeminiModel`, `ImageModel`, `KeyframeAspectRatio` and `KeyframeImageSize` — this library keeps no default model names and no art direction; model IDs rot on Google's release schedule and aspect ratio varies per work, so a default here would be a second source of truth for a value the caller already owns).

`video.ErrRecipeInvalid` sits in `video`, not `ports`, because `Recipe.Validate` returns it and `video` is the leaf package — it cannot import `ports`.

### Concurrency notes

Reference-image resolution used to live here as `keyframe.Composer` — a pre-upload pass with double-checked locking and `singleflight`, caching File API uploads. That whole problem is gone: on Vertex AI a `gs://` URI is handed to the model as-is, so there is nothing to fetch, upload or cache. **Do not reintroduce a pre-upload pass or a reference cache** — if one ever looks necessary, the real cause is a reference that stopped being `gs://`, and that is the thing to fix. **One reference image per cut is deliberate.** `Cut.CharacterID` is singular and `keyframe.Generator` attaches exactly one reference (the aspect-ratio-matched variant when the character has one), because multi-subject image generation is not reliable enough at the current model generation to be worth the schema change. The switch point when that changes is now just data: `ImageGenerator.Generate(ImageRequest)` takes `ImageRequest.Images` holding 0..n references, so attaching a second reference needs no API change at all. The likely first use is not multiple characters but **continuity**: attaching the previous cut's keyframe alongside the character reference for cuts that continue a chain (`ChainControl.IsChainStart` is false), the same trick go-comic-kit uses when composing a page from its panels.

**AI-call guards are split across two layers on purpose.** `workflow` owns the rate interval and the per-call timeout (`callGuard` in `singleflight.go`, fed by `Config.RateInterval` / `Config.RequestTimeout`) and applies them to **both** script text generation and keyframe image generation — Gemini quota is per project, not per operation kind, so guarding only the image path leaves the text path free to blow the same quota. The rate-limit wait happens *outside* the timeout, so a busy queue cannot manufacture timeouts. Those guards are deliberately **not** delegated to vertex-image-kit's `WithRateLimit` / `WithRequestTimeout`, which would only cover images. `internal/runner.CutKeyframeRunner` owns the concurrency limit instead (`Config.MaxConcurrency` → `runner.WithMaxConcurrency`, applied with `errgroup.SetLimit`). It lives on the runner rather than the keyframe generator because the runner also owns the saving, and one cut's generate-and-save has to stay inside one goroutine. Delegating to the image kit's `GenerateBatch` would also remove the per-image hook that emits progress logs — with a single keyframe taking minutes, "3/12, 47s" is how a run is monitored. Note that with a non-zero `RateInterval`, throughput is capped at `1/RateInterval` regardless of concurrency; setting both aggressively is contradictory.

Both AI paths are also wrapped in **singleflight decorators** (`workflow/singleflight.go`): identical in-flight requests collapse into one API call, shared responses are cloned per caller, and the shared execution runs on a detached context so one caller's cancel cannot kill the callers piggybacking on it. This dedupes within one process only — durable idempotency comes from the recipe's `keyframe_reference` / `video_id`. This mirrors go-comic-kit, which is where the pattern comes from.

A cut that fails does not discard the images already paid for: partial successes are kept and the failures are aggregated with `errors.Join`. `VideoTimelineRunner.Run`, by contrast, is strictly sequential per cut — the Video-to-Video chain requires each cut's `PreviousVideoURI` before the next can start.

## External dependencies worth knowing

- `github.com/shouni/vertex-image-kit` — keyframe / still-image generation on Vertex AI (`generator.New` → `*generator.Generator`); this repo's `imagePorts.ImageGenerator` / `ImageRequest` / `ImageResponse` / `ImageURI` come from its `ports` package. It accepts **`gs://` references only** and transfers no bytes, which is why this repo needs no GCS reader or HTTP client for images. It replaced `gemini-image-kit`, whose File API upload, cache and fetch layers never executed on the Vertex backend.
- `github.com/shouni/go-character-kit` — `characterkit.Characters`/`Character` (Seed, ReferenceURL(s)) for cross-cut character consistency.
- `github.com/shouni/go-gemini-client` — `music.Recipe` (aliased as `video.MusicRecipe`) is the input music/lyrics recipe format — it lives in the leaf `music` package, so importing it does not drag in the `lyria` workflow. AI clients are taken as `gemini.Generator` for both script and image generation — the narrowest genai-free interface, one method: nothing in this repo imports `google.golang.org/genai`. Structured output uses plain JSON Schema via `GenerateOptions.ResponseJSONSchema` (`video.RecipeSchema` returns `map[string]any`), not `genai.Schema`.
- `github.com/shouni/go-remote-io` — `remoteio.Writer` used for persisting `video_music_meta.json` and keyframe outputs.
