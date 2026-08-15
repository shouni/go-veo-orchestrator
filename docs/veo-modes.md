# 🎛️ Veo 生成モードとカット尺

[← README](../README.md)

1つのリクエストが Veo のどの生成機能で解釈されるかは、`veo.ClassifyRequest` **1箇所**で決まります。adapter のリクエスト本文構築、カット尺の計画・検証、生成モードごとのプロンプト選択は、すべてこの同じ判定を共有してください。

それぞれが独自に分岐すると「参照画像に合わせろと指示しながら参照画像を送らない」「reference_to_video 前提で8秒に丸めたのに実際は image_to_video だった」といったズレが起きます。

```go
caps := veo.RunnerCapabilities(videoRunner) // Runner のオプションインターフェースから導出
mode := veo.ClassifyRequest(req, usePreviousVideo, caps)
```

## 判定の優先順位と対応尺

モードは `veo.GenerationMode`、モデルの対応状況は `veo.Capabilities` です。尺の一覧は `ports.ImageToVideoDurationsSec` / `ports.ReferenceToVideoDurationsSec` として公開しています。

| 優先 | モード | 条件 | 対応尺（秒） |
| --- | --- | --- | --- |
| 1 | `VeoModeVideoExtension` | `usePreviousVideo` かつ `PreviousVideoURI` が `gs://` 参照。画像参照はすべて無視されます | 7 固定 |
| 2 | `VeoModeReferenceToVideo` | 参照画像が1つ以上あり、モデルが `referenceImages` 対応（Veo 3系の非 Fast） | 8 固定 |
| 3 | `VeoModeFramesToVideo` | 開始フレームと `LastFrameReference` が両方あり、モデルが `lastFrame` 対応（Veo 2 / Veo 3.1系） | 4 / 6 / 8 |
| 4 | `VeoModeImageToVideo` | 上記以外すべて | 4 / 6 / 8 |

モデルの対応状況を伝えるオプションインターフェースについては [Adapter Boundary](adapter.md#モデルの対応機能を伝える) を参照してください。

## 尺を計画するヘルパー

Veo は任意長の動画を生成できないため、レシピ側でこれらの値に合わせて尺を割り当ててから実行してください。

| API | 用途 |
| --- | --- |
| `veo.DurationsForMode(mode)` | そのモードで受け付けられる尺の一覧 |
| `veo.IsSupportedDuration(sec, mode)` | 尺が受け付けられるかの判定 |
| `veo.SnapDuration(sec, candidates)` | 最も近い対応尺へ丸める（同距離なら長い方） |
| `veo.ChainDurations(bases)` | 1本の継続チェーン（ベース + 7秒 × n）で実現できる合計尺の候補 |
| `veo.ContinuationMaxDurationSec` | チェーンをリセットする累積尺の閾値（24秒） |

`VideoTimelineRunner.Run` は各カットを Veo へ投げる直前にこの尺を検証し、対応外なら `ports.ErrUnsupportedCutDuration` を返してそのカットの生成を行いません。長時間実行 operation を投げて Veo 側に拒否されるまで待つより手前で、どのカットが何秒でどのモードだったかまで示して落とします。

## 参照画像の組み立て

参照画像（`referenceImages`）の組み立て規則は `video.CutReferenceImages(cut, characters)` に一本化されています。`[キャラクター立ち絵, キーフレーム]` の順に最大3枚で、立ち絵が無いカットはキーフレームだけを参照として使います。

カットを分類する側もリクエストを組み立てる側も、必ずこの関数を通してください。カットが丸められる尺と、そのリクエストが実際に解決するモードが一致しなくなります。
