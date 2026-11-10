# 🎬 Go Veo Orchestrator

[![CI](https://github.com/shouni/go-veo-orchestrator/actions/workflows/ci.yml/badge.svg)](https://github.com/shouni/go-veo-orchestrator/actions/workflows/ci.yml)
[![Status](https://img.shields.io/badge/Status-Active-brightgreen)](#)
[![Language](https://img.shields.io/badge/Language-Go-blue)](https://go.dev/)
[![Go Version](https://img.shields.io/github/go-mod/go-version/shouni/go-veo-orchestrator)](https://go.dev/)
[![GitHub tag (latest by date)](https://img.shields.io/github/v/tag/shouni/go-veo-orchestrator)](https://github.com/shouni/go-veo-orchestrator/tags)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Reference](https://pkg.go.dev/badge/github.com/shouni/go-veo-orchestrator.svg)](https://pkg.go.dev/github.com/shouni/go-veo-orchestrator)

English | [日本語](README.ja.md)

## 🚀 About - from a song's structure to a sequence of video cuts that keeps its context

**Go Veo Orchestrator** is a backend orchestrator that structures a **Music Recipe** (a song's composition sheet) into a sequence of video cuts and hands them to Google's video generation AI, **Veo (Vertex AI / Gemini API)**.

It generates a keyframe for each cut with `imagegen` from [genai-kit](https://github.com/shouni/genai-kit), and passes **Prompt / Keyframe / Audio / PreviousVideoURI / Seed** to Veo through a `VideoRunner` adapter.

By carrying each cut's `video_id` into the next cut as its `PreviousVideoURI`, it generates consecutive cuts that keep their Video-to-Video context.

---

## ✨ Features

* **🧬 Consistency Control**
  Bundles a **per-character Seed**, a **keyframe image**, a **motion prompt** and the **previous cut's VideoID** into one `video.GenerationRequest`, keeping both the look and the context consistent across cuts.

* **⏳ Audio-Driven Timeline Logic**
  Fills in `duration_sec`, `start_sec` and `end_sec` from `music_recipe.sections` or `cuts`, and injects each `audio_cue` into the Veo prompt.

* **🔁 Resumable Video Chain**
  Each `cut` carries `status`, `video_id` and `video_url`. Cuts already generated are not regenerated, and their stored `video_id` is used as the next cut's `PreviousVideoURI`.
  Keyframes follow the same idea: a cut that has a `keyframe_reference` is not re-baked (`CutKeyframe.GenerateAndSave`). **Each image is saved the moment it is generated**, so a crash loses at most one image and the run resumes where it stopped. To re-bake a cut, clear its `keyframe_reference` before passing it in (`Cut.ResetGeneration(false)`).

* **🧩 Adapter-Oriented Architecture**
  All actual communication with Veo is confined to `ports.VideoRunner`, keeping orchestration, keyframe generation and metadata persistence separate. **This repository contains no concrete Veo API implementation** ([implementation guide](docs/adapter.md)).

---

## 🎬 The four video generation workflows

| Workflow | Interface | What it does |
| --- | --- | --- |
| **1. Scripting** | `ScriptRunner` | Reads Music Recipe JSON and, from its lyrics, sections and song development, generates a **Video Recipe** with the cut breakdown, camera work and estimated durations. |
| **2. Cut Keyframe Gen** | `CutKeyframeRunner` | Generates and saves each cut's keyframe image using the character Seed and reference image (`GenerateAndSave`). Also supports localized edits to an existing keyframe (`EditAndSave`). |
| **3. Video Gen** | `VideoTimelineRunner` + `VideoRunner` | `VideoRequestBuilder` assembles each `video.GenerationRequest` and submits them to the Veo adapter in order. |
| **4. Metadata Publish** | `VideoPublishRunner` | Saves `video_music_meta.json` with `video_id` / `video_url` / `status` updated. |

---

## 🚦 Usage

Pass the dependencies to `workflow.New` to build a `*ports.Workflows`, then call each Runner.

```go
workflows, err := workflow.New(workflow.ManagerArgs{
	// Required: the kit holds neither model names nor art-direction defaults.
	Config: ports.Config{
		GeminiModel:            "gemini-3.6-flash",
		ImageModel:             "gemini-3.1-flash-image",
		KeyframeAspectRatio:    "16:9",
		KeyframeImageSize:      "2K",
		KeyframeNegativePrompt: "speech bubble, text, watermark", // optional
	},
	Reader:      reader, // fetches the script source (not used for images)
	Writer:      writer, // must be safe for concurrent use, because saving runs in parallel
	AIClient:    geminiClient,
	VideoRunner: &VeoRunner{}, // your own Veo adapter
	PromptDeps:  promptDeps,
})
if err != nil {
	return err
}

// Generate and save the keyframes before generating video. Video does not
// create keyframes (it only reads the saved keyframe_reference).
if _, err := workflows.CutKeyframe.GenerateAndSave(ctx, recipe, "gs://bucket/jobs/<jobID>/"); err != nil {
	return err
}

videos, err := workflows.Video.Run(ctx, recipe)
if err != nil {
	return err
}

// Saving the metadata is Publish's job. Generating video and saving are kept
// apart so the caller can insert a step in between (joining the chains, etc.).
if _, err := workflows.Publish.Run(ctx, recipe, "gs://bucket/jobs/<jobID>/"); err != nil {
	return err
}
```

---

## 📚 Documentation

| Document | Contents |
| --- | --- |
| [Music Recipe JSON](docs/music-recipe.md) | Input format, automatic generation of `cuts`, `section_index`, the structure of `video.Cut` |
| [Configuration and DI](docs/configuration.md) | `ports.Config` / `ManagerArgs` / `PromptDeps`, keyframe generation options |
| [Adapter Boundary](docs/adapter.md) | Implementation guide for `ports.VideoRunner` and the `video.GenerationRequest` contract |
| [Veo generation modes and cut durations](docs/veo-modes.md) | Classification by `veo.ClassifyRequest`, durations per mode, the duration planner |
| [Working with recipes and cuts](docs/recipe-api.md) | Resume/regenerate helpers, partial results, single-cut keyframe editing |
| [Sentinel Errors](docs/errors.md) | The errors to branch on with `errors.Is` |
| [Architecture](docs/architecture.md) | Package layout, the split between generating and saving, sequence diagrams |

---

## 🤝 Dependencies

* [shouni/genai-kit](https://github.com/shouni/genai-kit) - Vertex AI client (`gemini`), keyframe generation (`imagegen`), call guards (`callguard`), the song recipe type (`music`). Reference images are passed to Vertex AI as `gs://` URIs, unchanged
* [shouni/go-character-kit](https://github.com/shouni/go-character-kit) - character asset (characters.json) management
* [shouni/go-remote-io](https://github.com/shouni/go-remote-io) - read/write abstraction over GCS and local storage

## 📜 License

This project is released under the [MIT License](https://opensource.org/licenses/MIT).
