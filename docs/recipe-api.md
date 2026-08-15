# 🔧 レシピ・カットの操作

[← README](../README.md)

`VideoRecipe` / `Cut` は、再開・再生成のために状態を読み書きするヘルパーを持ちます。一括生成を途中から再開する処理は、これらを使って「どのカットがまだか」を判断します。

## ヘルパー一覧

| API | 用途 |
| --- | --- |
| `Cut.IsGenerated()` / `Cut.Status`（`ports.CutStatus`） | そのカットが生成済みかの判定と状態 |
| `Cut.ResetGeneration(keepKeyframe)` | 生成結果を捨てて再生成対象に戻す。`keepKeyframe=false` は `keyframe_reference` も消すため、次の `RunAndSave` で画像から焼き直されます |
| `Cut.EffectiveDurationSec()` | そのカットの実効尺 |
| `VideoRecipe.Normalize()` | カットの連番・開始秒・`SectionIndex` / `LocationAnchor` の伝播を整えます |
| `VideoRecipe.Validate()` | レシピの整合性検証 |
| `Config.UsesModels(gemini, image)` / `Cuts.UniqueCharacterIDs()` | 使用モデルの一致判定・登場キャラクターの集合 |
| `Cuts.NextLastFrameReference(i)` | frames_to_video で次カットの `lastFrame` に使う参照の解決 |
| `ports.VideoRecipeSchema(characterIDs)` | 台本生成の構造化出力スキーマ（`character_id` を enum で制約した JSON Schema） |
| `Cuts.FillAudioReference(url)` / `Cuts.FillCharacterID(id)` | 未設定カットへの音源・キャラクターの一括補完 |
| `Cuts.IndexOf(cutIndex)` | cut_index からレシピ内の位置を引く |
| `ports.NewVideoRecipeFromMusic(music.Recipe)` | Music Recipe から動画レシピを組み立て（深いコピー + Normalize） |
| `ports.ExpandCutsToSupportedDurations(...)` | カット尺を Veo のサポート値へ正規化する尺プランナー（分割・チェーン形成・セクション境界リセット） |
| `ports.CapCutsTotalDuration(cuts, maxSec)` / `ports.ChainTailEnd(cuts, i, usePrev)` | 合計尺の切り詰め・チェーン再生成範囲の解決 |
| `ports.SplitCutBySupportedDurations(cut, totalSec)` / `ports.AllowedCutDurations(usesRefs)` | 1 カットの分割と、参照画像の有無で変わる許容尺の取得 |
| `ports.CutUsesReferenceImages(cut, characters)` | そのカットが reference_to_video 経路（＝8 秒固定）になるかの判定 |
| `ports.SplitDialogueLines(dialogue)` / `ports.DistributeLines(lines, n)` | 台詞の行分割と、分割後カットへの割り当て |
| `ports.VeoModelCapabilities(model)` | モデル名から referenceImages / lastFrame 対応を導出 |

## 部分結果とライフサイクル

`workflow.New` が返す `*ports.Workflows` に `Close()` はありません。参照画像を `gs://` URI のまま渡すようになり、停止すべき画像キャッシュの goroutine が無くなったためです。公開・保存の出力は `ports.PublishResult` です。

`VideoTimelineRunner.Run` は**エラー時も完了済みカットの部分結果を返します**。`WithCutObserver` でカット完了ごとのフック（メタデータ保存や実行時間予算の確認）を差し込め、フックがエラーを返すとそこで停止して部分結果を返します。時間制限のあるジョブ基盤で「一旦保存して次の実行で再開」する運用が、自前のカットループなしで組めます。

同様に `CutKeyframeRunner.RunAndSave` も、生成が途中で失敗した場合に**成功した分の画像とメタデータを保存してから**エラーを返します。

## 🩹 単一カットのキーフレーム編集 (EditAndSave)

`CutKeyframeRunner.RunAndSave` はプロンプトから画像を作り直す「フル生成」ですが、`EditAndSave` は既存のキーフレーム画像を編集元として、テキスト指示で局所的な修正だけを反映します。構図・ポーズ・背景は保たれるため、同じキャラクターの他カットとの一貫性を保ったまま「小物の数を減らす」「色味を揃える」といった軽微な修正に向いています。

レシピ全体と、その中の対象カットの位置（0 始まり）を渡します。対象カットの `KeyframeReference` は、編集元となる既存のキーフレーム画像を指している必要があります。

```go
// recipe は完全なレシピのままで構いません。編集するカットは位置で指定します。
const cutPosition = 1 // recipe.Cuts[1] を編集する

updated, err := workflows.CutKeyframe.EditAndSave(ctx, recipe, cutPosition,
	"腕には絆創膏を1〜2枚のみにしてください", "gs://bucket/jobs/j1/regens/cut-2/")
if err != nil {
	return err
}
// updated.Cuts[cutPosition].KeyframeReference が編集後の画像パスに更新されています。
```

対象カットだけの使い捨てレシピを組む必要はありません（v1.11 より前はそれが必須で、カットごとにループする呼び出し側がレシピを組み直していました）。保存されるメタデータも常に完全なレシピになります。

内部的には [vertex-image-kit](https://github.com/shouni/vertex-image-kit) の `ImageGenerator.Generate` に既存キーフレーム画像を入力として渡し、`editPrompt` をプロンプトとして呼び出します。`RunAndSave`（通常のキーフレーム生成）と同じ会話型マルチモーダル画像モデル（`Config.ImageModel`、Gemini の「Nano Banana」系）をそのまま再利用するため、編集専用のモデルや API は不要です。

> Vertex AI Imagen のマスクベース編集/カスタマイズ API（`imagen-3.0-capability-001` 系）は2026年6月30日に廃止され、後継の「capability」モデルも用意されていません。そのため `EditAndSave` はマスク指定には対応せず、自由記述の編集指示のみをサポートします。

エラーになる条件:

* `cutPosition` が `recipe.Cuts` の範囲外の場合
* 対象カットの `KeyframeReference` が空の場合（＝編集元画像がない）は `ports.ErrNoKeyframeToEdit`

キャラクターの Seed は `RunAndSave` と同様、`char.Seed` がそのまま編集リクエストに使われます。
