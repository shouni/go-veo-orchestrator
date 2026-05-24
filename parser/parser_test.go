package parser

import (
	"context"
	"io"
	"strings"
	"testing"
)

// --- Mocks ---

// mockReader は ports.ContentReader インターフェースを満たすテスト用モックです。
type mockReader struct {
	openFunc func(ctx context.Context, path string) (io.ReadCloser, error)
}

// Open は ports.ContentReader インターフェースの実装です。
func (m *mockReader) Open(ctx context.Context, path string) (io.ReadCloser, error) {
	return m.openFunc(ctx, path)
}

// stringReadCloser は文字列を io.ReadCloser として扱うためのヘルパーです。
type stringReadCloser struct {
	*strings.Reader
}

func (s *stringReadCloser) Close() error { return nil }

// --- Tests ---

func TestMangaResponseParser_ParseFromPath(t *testing.T) {
	ctx := context.Background()

	t.Run("Success with Valid JSON", func(t *testing.T) {
		validJSON := `{
			"title": "テスト漫画",
			"description": "これはテストです",
			"panels": [
				{"speaker_id": "zundamon", "dialogue": "こんにちは！"},
				{"speaker_id": "metan", "dialogue": "ごきげんよう。"}
			]
		}`

		mReader := &mockReader{
			openFunc: func(ctx context.Context, path string) (io.ReadCloser, error) {
				return &stringReadCloser{strings.NewReader(validJSON)}, nil
			},
		}

		// Reader を受け取る Parser の初期化
		p := NewMangaResponseParser(mReader)
		res, err := p.ParseFromPath(ctx, "gs://bucket/plot.json")

		if err != nil {
			t.Fatalf("ParseFromPath failed: %v", err)
		}

		if res.Title != "テスト漫画" {
			t.Errorf("Expected title 'テスト漫画', got '%s'", res.Title)
		}
		if len(res.Panels) != 2 {
			t.Errorf("Expected 2 panels, got %d", len(res.Panels))
		}
		if res.Panels[0].SpeakerID != "zundamon" {
			t.Errorf("Expected first speaker 'zundamon', got '%s'", res.Panels[0].SpeakerID)
		}
	})

	t.Run("Failure with Invalid JSON", func(t *testing.T) {
		invalidJSON := `{ "title": "incomplete json...`

		mReader := &mockReader{
			openFunc: func(ctx context.Context, path string) (io.ReadCloser, error) {
				return &stringReadCloser{strings.NewReader(invalidJSON)}, nil
			},
		}

		p := NewMangaResponseParser(mReader)
		_, err := p.ParseFromPath(ctx, "invalid.json")

		if err == nil {
			t.Error("Expected error for invalid JSON, but got nil")
		}
		if !strings.Contains(err.Error(), "パースに失敗しました") {
			t.Errorf("Unexpected error message: %v", err)
		}
	})

	t.Run("Failure when File Open Fails", func(t *testing.T) {
		mReader := &mockReader{
			openFunc: func(ctx context.Context, path string) (io.ReadCloser, error) {
				return nil, io.ErrUnexpectedEOF // オープン失敗をシミュレート
			},
		}

		p := NewMangaResponseParser(mReader)
		_, err := p.ParseFromPath(ctx, "non-existent.json")

		if err == nil {
			t.Error("Expected error for file open failure, but got nil")
		}
		if !strings.Contains(err.Error(), "オープンに失敗しました") {
			t.Errorf("Unexpected error message: %v", err)
		}
	})
}
