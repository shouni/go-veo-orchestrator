package parser

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/shouni/go-veo-orchestrator/ports"
)

// Parser は解析するためのインターフェースを定義します。
type Parser interface {
	ParseFromPath(ctx context.Context, fullPath string) (*ports.MangaResponse, error)
}

// MangaResponseParser は JSON 形式の台本を解析する構造体です。
type MangaResponseParser struct {
	reader ports.ContentReader
}

// NewMangaResponseParser は新しい MangaResponseParser インスタンスを生成します。
func NewMangaResponseParser(r ports.ContentReader) *MangaResponseParser {
	return &MangaResponseParser{reader: r}
}

// ParseFromPath は指定された GCS URIやローカルファイルパスなどから
// コンテンツを読み込み、解析して domain.MangaResponse を返します。
func (p *MangaResponseParser) ParseFromPath(ctx context.Context, plotFile string) (*ports.MangaResponse, error) {
	slog.InfoContext(ctx, "プロットファイルを読み込んでいます", "path", plotFile)
	rc, err := p.reader.Open(ctx, plotFile)
	if err != nil {
		return nil, fmt.Errorf("プロットファイルのオープンに失敗しました (%s): %w", plotFile, err)
	}
	defer rc.Close()

	manga := &ports.MangaResponse{}
	if err := json.NewDecoder(rc).Decode(manga); err != nil {
		return nil, fmt.Errorf("プロットJSONのパースに失敗しました: %w", err)
	}

	return manga, nil
}
