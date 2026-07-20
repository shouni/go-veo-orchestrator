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
           (VideoRecipe, Cut, VideoGenerationRequest), Config, sentinel errors. Everything
           else depends on this package; it depends on nothing else in-repo.
keyframe/  Composer (uploads/caches character reference images to the File API or resolves
           GCS URIs directly under Vertex AI) + Generator (concurrent, rate-limited keyframe
           image generation per cut).
runner/    Concrete Runner implementations: VideoScriptRunner, CutKeyframeRunner,
           VideoTimelineRunner (+ VideoRequestBuilder), VideoPublisherRunner.
workflow/  manager: workflow.New(ManagerArgs) builds the generationUnit (image core/composer/
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

### Adapter boundary (`ports.VideoRunner`)

The real Veo/Vertex AI call is entirely external to this repo. An adapter's `Run(ctx, VideoGenerationRequest) (*VideoResponse, error)` is responsible for auth, resolving `ImageReference`/`AudioReference` (preferred) vs. uploading `InputImage`/`InputAudio` (fallback when the reference is empty), submitting to Veo, polling long-running operations, and returning `VideoID` (needed to chain into the next cut's `PreviousVideoID`) and `CloudURL`. `ReferenceImages` (max 3, `characterkit` character art + keyframe) takes priority over `ImageReference`/`InputImage` when both are supplied by `DefaultVideoRequestBuilder`. `LastFrameReference` is only meaningful for image-to-video Veo 2 / Veo 3.1 requests and must be paired with a start frame.

### Sentinel errors (`ports/errors.go`)

Callers use `errors.Is` against these to branch on specific failure modes rather than treating all errors alike: `ErrRecipeRequired`, `ErrEditingNotSupported` (image generator doesn't implement `EditCut`; caller can fall back to full `RunAndSave` regeneration), `ErrInvalidAIResponse` (AI text didn't parse as VideoRecipe JSON — distinguish from network/auth errors when deciding to retry), `ErrVideoRunnerNotConfigured`, `ErrInputTooLarge`.

### Concurrency notes

`keyframe.Composer` uses double-checked locking + `singleflight` to dedupe concurrent uploads of the same character reference image, and skips upload entirely under Vertex AI when the reference is already a `gs://` URI. `keyframe.Generator` runs cut keyframe generation concurrently with a configurable `MaxConcurrency` and `RateInterval` (see `keyframe.WithMaxConcurrency`, `keyframe.WithRateInterval` in `workflow/runners.go`). `VideoTimelineRunner.Run`, by contrast, is strictly sequential per cut — the Video-to-Video chain requires each cut's `PreviousVideoID` before the next can start.

## External dependencies worth knowing

- `github.com/shouni/gemini-image-kit` — keyframe/still-image generation core (`generator.GeminiImageCore`, `generator.GeminiGenerator`); this repo's `imagePorts.ImageGenerator`/`AssetManager`/`Backend` types come from here.
- `github.com/shouni/go-character-kit` — `characterkit.Characters`/`Character` (Seed, ReferenceURL(s)) for cross-cut character consistency.
- `github.com/shouni/go-gemini-client` — `lyria.MusicRecipe` (aliased as `ports.MusicRecipe`) is the input music/lyrics recipe format; `gemini.GenerativeModel` is the AI client interface used for script generation.
- `github.com/shouni/go-remote-io` — `remoteio.Writer` used for persisting `video_music_meta.json` and keyframe outputs.
