package video

// GenerationRequest は Veo API に渡すマルチモーダルな入力をカプセル化します。
type GenerationRequest struct {
	Prompt string
	// ImageReference があれば Veo adapter はこれを優先します。
	// 空の場合だけ InputImage をアップロードして参照 URI を作る想定です。
	ImageReference string
	// ReferenceImages はキャラクター立ち絵やキーフレームなど複数の参照画像 GCS URI です。
	// Veo API の referenceImages フィールドに対応し、最大3枚まで指定できます。
	// セットされている場合は ImageReference より優先されます。
	ReferenceImages []string
	AudioReference  string
	InputImage      []byte
	InputAudio      []byte
	// PreviousVideoURI は前カットの Video-to-Video 文脈として引き継ぐ動画の
	// gs:// URI です。Veo の video 入力は GCS 参照のみを受け付けるため、
	// gs:// 以外の値（オペレーション名など）を入れても video_extension には
	// 分類されません（veo.ClassifyRequest / DefaultVideoRequestBuilder が除去します）。
	// Veo API は video と referenceImages を同時に受け付けないため、
	// usePreviousVideo が有効な場合のみ adapter 側で使用します。
	PreviousVideoURI string
	// LastFrameReference は動画の終了フレームとして使う画像の GCS URI です
	// （Veo の first/last frame 補間）。Veo API では image（開始フレーム）との
	// 併用が必須のため、adapter 側は image 入力（image_to_video）のときだけ
	// lastFrame として送ります。対応モデルは Veo 2 / Veo 3.1 系のみです。
	//
	// 値は runner.BuildInput.LastFrameReference として Build() へ渡します
	// （通常は Cuts.NextLastFrameReference の戻り値。セクション境界・キャラクター
	// 変更・同一キーフレームといった last-frame ガードはそちらが持ちます）。
	// frames_to_video に解決しないリクエストでは Build() が落とすため、
	// このフィールドが残っているリクエストは必ず lastFrame を実際に使います。
	LastFrameReference string
	Seed               int64
	CutIndex           int
	DurationSec        float64
}

// Response は生成された動画のメタデータです。
type Response struct {
	CloudURL string
	// VideoID は生成された動画の識別子で、次カットの PreviousVideoURI として
	// そのまま渡せる gs:// URI であることが契約です。GCS 出力を持たない生成
	// （URI が得られない場合）は空にしてください — オペレーション名などで埋めると、
	// 次カットが video_extension に分類されず、7秒固定で計画された尺が
	// ErrUnsupportedCutDuration で拒否されます。
	VideoID     string
	CutIndex    int
	DurationSec float64
	MimeType    string
	SizeBytes   int64
}
