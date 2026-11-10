# 🧾 Music Recipe JSON

[← README](../README.md)

This page describes the library's input format and the structure of `VideoRecipe` / `Cut`.

## Script generation input and output

`ScriptRunner` interprets the Music Recipe JSON at `sourceURL` as a `VideoRecipe` and passes the parsed object to the prompt builder. From `music_recipe.lyrics` / `music_recipe.sections`, the prompt builder generates video-script JSON that includes the BGM's meter, emotion and build-ups.

The lyric text is kept in `music_recipe.lyrics`, but it is not injected into the Veo prompt directly. The prompt builder in the script generation stage expands the meaning of the lyrics and sections into `cuts[].audio_cue` and `cuts[].visual_anchor`. Whatever lyric content should appear in the video therefore has to be converted into `cuts`.

Each `cut` has `duration_sec` and `audio_cue`, so sync directions such as `(synchronized with the heavy bass drop at 0:10)` can be injected into the Veo prompt automatically. The application's job is to display and edit the generated `cuts` and pass them to the keyframe generation or video generation form.

The prompt that reaches Veo is built from `cuts[].visual_anchor`, `cuts[].audio_cue`, `music_recipe.mood` and the timeline information.

## A complete example

The values are sample data from a Japanese project; the field names are what matter.

```json
{
  "project_title": "AIマルチモーダル解説動画",
  "music_recipe": {
    "title": "AIマルチモーダル解説動画",
    "theme": "AIマルチモーダル解説",
    "mood": "90s retro mech synthwave",
    "tempo": 120,
    "lyrics": {
      "title": "AIマルチモーダル解説動画",
      "theme": "AIマルチモーダル解説",
      "hook": "未来の映像制作をひらく",
      "lyrics": "[Verse] 画面の奥で光が走る\n[Chorus] 未来のカットが動き出す",
      "keywords": [
        "AI",
        "video",
        "orchestration"
      ],
      "mood": "90s retro mech synthwave",
      "narrative": "AI が映像制作の工程をつなぐ物語"
    },
    "instruments": [
      "analog synth",
      "electronic drums"
    ],
    "sections": [
      {
        "name": "Intro",
        "duration_seconds": 5,
        "prompt": "quiet synth pad and clock tick"
      },
      {
        "name": "Verse",
        "duration_seconds": 5,
        "prompt": "drum beat starts and tempo lifts"
      },
      {
        "name": "Chorus",
        "duration_seconds": 5,
        "prompt": "bright synth lead and impact effects"
      }
    ]
  },
  "cuts": [
    {
      "cut_index": 1,
      "duration_sec": 5,
      "audio_cue": "イントロ：静かなシンセのパッド音、秒針の音 (mp3_segment_1)",
      "visual_anchor": "暗闇の中にキャラクターの瞳が光る。カメラがゆっくりと引いていく",
      "character_id": "zundamon"
    },
    {
      "cut_index": 2,
      "duration_sec": 5,
      "audio_cue": "Aメロ：歌詞の導入に合わせてドラムのビートが刻まれ始める。テンポアップ (mp3_segment_2)",
      "visual_anchor": "歌詞の「画面の奥で光が走る」を、ずんだもんの背後に走る光のラインとして映像化する",
      "character_id": "zundamon"
    },
    {
      "cut_index": 3,
      "duration_sec": 5,
      "audio_cue": "サビ：歌詞の hook に合わせて激しいシンセのメロディとエフェクト音が入る (mp3_segment_3)",
      "visual_anchor": "歌詞の「未来のカットが動き出す」を、カメラが高速旋回しながらサイバー空間へ切り替わる動きで表現する",
      "character_id": "zundamon_metan"
    }
  ]
}
```

`Normalize()` fills in `cut_index` / `start_sec` / `end_sec` / `status` / `section_index` on this JSON, and copies the recipe-level `location_anchor` / `aspect_ratio` onto each cut. After generation it is saved as `video_music_meta.json` with `keyframe_reference`, `video_id` and `video_url` added.

## The minimal form, without cuts

When `cuts` is empty, the cut sequence is generated automatically from `music_recipe.sections`. `music_recipe` holds `github.com/shouni/genai-kit/music.Recipe` as-is, so the music generation side's JSON goes under `music_recipe`.

```json
{
  "project_title": "AIマルチモーダル解説動画",
  "music_recipe": {
    "title": "AIマルチモーダル解説動画",
    "theme": "AIマルチモーダル解説",
    "mood": "upbeat electronic documentary score",
    "tempo": 120,
    "instruments": [
      "analog synth",
      "electronic drums",
      "soft piano"
    ],
    "sections": [
      {
        "name": "Verse",
        "duration_seconds": 40,
        "prompt": "quiet opening with restrained melody and gradual rhythmic build"
      },
      {
        "name": "Chorus",
        "duration_seconds": 45,
        "prompt": "emotional peak with fuller instrumentation and stronger accents"
      }
    ],
    "audio_model": "lyria-3-pro-preview",
    "compose_mode": "game_fantasy",
    "seed": 10
  },
  "cuts": []
}
```

## section_index — mapping cuts to sections

Each `cut` keeps, in `section_index` (1-based), the position in `music_recipe.sections` it was derived from. When one section is split into several cuts by `scene_split` or similar, every resulting cut inherits the same `section_index`. Callers can determine a cut's section directly, without reverse-matching `start_sec` against the sections' time ranges. For cuts that do not set it explicitly, `Normalize()` fills it in from `start_sec`.

## ⚠️ The internal structure of `video.Cut`

The JSON of a `Cut` stays flat, but as a Go struct it is split into `AudioSync` / `KeyframeResult` / `Result` / `ChainControl`, embedded as anonymous fields. Field access such as `cut.VideoID` is unchanged, but composite literals must be written group by group.

```go
// ✗ Cannot be written flat
video.Cut{DurationSec: 5, KeyframeReference: "..."}

// ✓ Written group by group
video.Cut{
	AudioSync:      video.AudioSync{DurationSec: 5},
	KeyframeResult: video.KeyframeResult{KeyframeReference: "..."},
}
```
