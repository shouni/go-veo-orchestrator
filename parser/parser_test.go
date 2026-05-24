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

	t.Run("Success with Music Recipe JSON", func(t *testing.T) {
		validJSON := `{
			"project_title": "AIマルチモーダル解説動画",
			"music_recipe": {
				"tempo_bpm": 120,
				"total_duration_sec": 15,
				"style": "90s retro mech synthwave"
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
					"audio_cue": "Aメロ：ドラムのビートが刻まれ始める。テンポアップ (mp3_segment_2)",
					"visual_anchor": "ずんだもんが自信満々に人差し指を立てて、カメラに向かって喋る",
					"character_id": "zundamon"
				}
			]
		}`

		mReader := &mockReader{
			openFunc: func(ctx context.Context, path string) (io.ReadCloser, error) {
				return &stringReadCloser{strings.NewReader(validJSON)}, nil
			},
		}

		p := NewMangaResponseParser(mReader)
		res, err := p.ParseFromPath(ctx, "gs://bucket/video_music_meta.json")
		if err != nil {
			t.Fatalf("ParseFromPath failed: %v", err)
		}

		if res.ProjectTitle != "AIマルチモーダル解説動画" {
			t.Errorf("Expected project title, got '%s'", res.ProjectTitle)
		}
		if len(res.Cuts) != 2 {
			t.Fatalf("Expected 2 cuts, got %d", len(res.Cuts))
		}
		if res.Cuts[1].StartSec != 5 || res.Cuts[1].EndSec != 10 {
			t.Errorf("Expected second cut range 5-10, got %.1f-%.1f", res.Cuts[1].StartSec, res.Cuts[1].EndSec)
		}
		if res.Cuts[0].SpeakerID != "zundamon" {
			t.Errorf("Expected compatibility speaker ID, got '%s'", res.Cuts[0].SpeakerID)
		}
	})

	t.Run("Success with Variable Sections Music Document", func(t *testing.T) {
		validJSON := `{
			"title": "碧き残影、一瞬の奇跡 〜黒き疾風の叙事詩〜",
			"theme": "闇を裂き、最速の奇跡を刻む青き瞳の誓い",
			"mood": "Epic Symphonic Fantasy Rock Ballad, Emotional and Melancholic",
			"tempo": 72,
			"instruments": [
				"Acoustic Grand Piano",
				"Soaring Full Strings Section"
			],
			"sections": [
				{
					"name": "Verse",
					"duration_seconds": 40,
					"prompt": "[Silent Awakening] Focus strictly on the first lyrics block marked [Verse]."
				},
				{
					"name": "Chorus",
					"duration_seconds": 45,
					"prompt": "[Emotional Outburst & High-Voltage Peak] Focus on the lyrics marked [Chorus]."
				},
				{
					"name": "Chorus 2",
					"duration_seconds": 55,
					"prompt": "[The Final Peak & Ultimate Triumphant Finale] Focus on the final lyrics marked [Chorus 2]."
				}
			],
			"lyrics": {
				"title": "碧き残影、一瞬の奇跡",
				"theme": "静寂を切り裂く、青き瞳の黒馬が駆け抜ける約束の道",
				"hook": "解き放て、闇を割く蒼き疾風（かぜ）を",
				"lyrics": "[Verse]\n蒼き仮面に 瞳を潜め",
				"keywords": ["黒鹿毛", "ブルーのメンコ"],
				"mood": "高揚と疾走感に満ちたファンタジー",
				"narrative": "青い仮面を纏う黒き名馬が、奇跡の末脚で伝説の頂点へと駆け上がる。"
			},
			"audio_model": "lyria-3-pro-preview",
			"compose_mode": "game_fantasy",
			"seed": 10
		}`

		mReader := &mockReader{
			openFunc: func(ctx context.Context, path string) (io.ReadCloser, error) {
				return &stringReadCloser{strings.NewReader(validJSON)}, nil
			},
		}

		p := NewMangaResponseParser(mReader)
		res, err := p.ParseFromPath(ctx, "gs://bucket/music_recipe.json")
		if err != nil {
			t.Fatalf("ParseFromPath failed: %v", err)
		}

		if res.ProjectTitle != "碧き残影、一瞬の奇跡 〜黒き疾風の叙事詩〜" {
			t.Errorf("Expected title to become project title, got '%s'", res.ProjectTitle)
		}
		if res.MusicRecipe.TempoBPM != 72 {
			t.Errorf("Expected tempo 72, got %d", res.MusicRecipe.TempoBPM)
		}
		if res.MusicRecipe.TotalDurationSec != 140 {
			t.Errorf("Expected total duration 140, got %.1f", res.MusicRecipe.TotalDurationSec)
		}
		if len(res.Cuts) != 3 {
			t.Fatalf("Expected 3 cuts from sections, got %d", len(res.Cuts))
		}
		if res.Cuts[2].StartSec != 85 || res.Cuts[2].EndSec != 140 {
			t.Errorf("Expected third cut range 85-140, got %.1f-%.1f", res.Cuts[2].StartSec, res.Cuts[2].EndSec)
		}
		if res.Cuts[0].VisualAnchor != "Verse" {
			t.Errorf("Expected section name as visual anchor, got '%s'", res.Cuts[0].VisualAnchor)
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
