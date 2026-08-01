package keyframe

import (
	"testing"

	characterkit "github.com/shouni/go-character-kit/character"
)

// mustNewCharacters はテスト用のキャラクター定義を組み立てます。
func mustNewCharacters(t *testing.T, list []characterkit.Character) *characterkit.Characters {
	t.Helper()

	chars, err := characterkit.NewCharacters(list)
	if err != nil {
		t.Fatalf("NewCharacters failed: %v", err)
	}

	return chars
}
