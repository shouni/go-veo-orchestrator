# 🧾 Music Recipe JSON

[← README](../README.md)

このライブラリの入力フォーマットと、`VideoRecipe` / `Cut` の構造を説明します。

## 台本生成の入出力

`ScriptRunner` は `sourceURL` の Music Recipe JSON を `VideoRecipe` として解釈し、prompt builder へ parsed object を渡します。prompt builder は `music_recipe.lyrics` / `music_recipe.sections` から、BGM の拍子・感情・盛り上がりを含む動画台本 JSON を生成します。

歌詞本文は `music_recipe.lyrics` に保存されますが、Veo prompt へ直接は注入されません。歌詞や section の意味は、script generation stage の prompt builder が `cuts[].audio_cue` と `cuts[].visual_anchor` に展開します。したがって、動画化したい歌詞の内容は `cuts` へ変換しておく必要があります。

各 `cut` は `duration_sec` と `audio_cue` を持つため、Veo へのプロンプトには `(synchronized with the heavy bass drop at 0:10)` のような同期指示を自動注入できます。アプリ側の責務は、生成された `cuts` を表示・編集し、キーフレーム生成または動画生成フォームへ渡すことです。

Veo に渡る prompt は `cuts[].visual_anchor`、`cuts[].audio_cue`、`music_recipe.mood`、タイムライン情報から構築されます。

## 完全な例

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

この JSON は `Normalize()` により `start_sec` / `end_sec` / `status` が補完されます。生成後は `keyframe_reference`、`video_id`、`video_url` が追記された `video_music_meta.json` として保存されます。

## cuts を省略した最小形

`cuts` が空の場合は、`music_recipe.sections` からカット列を自動生成します。`music_recipe` は `github.com/shouni/go-gemini-client/music.Recipe` をそのまま保持するため、楽曲生成側の JSON は `music_recipe` 配下へ入れます。

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
    "AudioModel": "lyria-3-pro-preview",
    "ComposeMode": "game_fantasy",
    "Seed": 10
  },
  "cuts": []
}
```

## section_index — カットとセクションの対応

各 `cut` は `section_index`（1始まり）で、由来となった `music_recipe.sections` の位置を保持します。1セクションが `scene_split` 等で複数カットに分割されても、分割後の全カットが同じ `section_index` を引き継ぎます。呼び出し側は `start_sec` とセクションの時間範囲を突き合わせて逆算せずに、カットの所属セクションを直接判定できます。明示的に設定されていないカットは、`Normalize()` が `start_sec` から自動的に補完します。

## ⚠️ `ports.Cut` の内部構造

`Cut` の JSON はフラットな構造のままですが、Go の構造体としては `AudioSync` / `KeyframeResult` / `VideoResult` / `ChainControl` へ分割され、匿名フィールドとして埋め込まれています。`cut.VideoID` のようなフィールドアクセスは変わりませんが、コンポジットリテラルはグループ単位で書く必要があります。

```go
// ✗ フラットには書けません
ports.Cut{DurationSec: 5, KeyframeReference: "..."}

// ✓ グループ単位で書きます
ports.Cut{
	AudioSync:      ports.AudioSync{DurationSec: 5},
	KeyframeResult: ports.KeyframeResult{KeyframeReference: "..."},
}
```
