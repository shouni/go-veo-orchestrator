package workflow

import (
	"fmt"

	"github.com/shouni/go-gemini-client/gemini"
	"github.com/shouni/vertex-image-kit/generator"
)

// buildGenerationUnit は画像生成の実行体をまとめた内部ユニットを構築します。
//
// 参照画像は gs:// URI をそのまま Vertex AI へ渡すため、GCS リーダーも HTTP
// クライアントもキャッシュも要りません。発射間隔と1回あたりの上限時間は
// vertex-image-kit のオプション（WithRateLimit / WithRequestTimeout）ではなく
// callGuard で掛けます — 同じリミッターを台本のテキスト生成にも共有する必要が
// あるためです。クォータはプロジェクト単位なので、画像だけ絞っても足りません。
func (m *manager) buildGenerationUnit(client gemini.Generator, modelName string, guard callGuard) (*generationUnit, error) {
	gen, err := generator.New(client)
	if err != nil {
		return nil, fmt.Errorf("画像生成エンジンの初期化に失敗しました: %w", err)
	}

	return &generationUnit{
		// 同一内容の画像生成の同時実行を1回にまとめる（重複タスク・リトライ対策）
		imageGenerator: &singleflightImageGenerator{inner: gen, guard: guard},
		model:          modelName,
	}, nil
}
