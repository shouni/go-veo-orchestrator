package ports

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// GetCharacter は、指定されたID（またはその小文字版）からキャラクター情報を特定します。
// マップに存在する場合はそのポインタを返し、存在しない場合は nil を返します。
func (m CharactersMap) GetCharacter(ID string) *Character {
	if m == nil {
		return nil
	}

	// 1. 直接のIDで検索、見つからなければ小文字に正規化して再検索
	char, ok := m[ID]
	if !ok {
		char, ok = m[strings.ToLower(ID)]
	}

	if ok {
		// マップから取得した値（コピー）のアドレスを直接返します
		return &char
	}

	return nil
}

// GetDefault はマップ内から IsDefault が true のキャラクターを1人返します。
// 常に決定論的な結果を得るため、IDでソートした順に走査します。
func (m CharactersMap) GetDefault() *Character {
	if len(m) == 0 {
		return nil
	}

	// キー（ID）を抽出してソート
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// ソート順に走査して最初に見つかった デフォルト を返す
	for _, k := range keys {
		char := m[k]
		if char.IsDefault {
			return &char
		}
	}

	return nil
}

// GetCharacterWithDefault は、指定されたIDでキャラクターを検索し、見つからない場合はデフォルトのキャラクターを返します。
// どちらのキャラクターも見つからない場合は nil を返します。
func (m CharactersMap) GetCharacterWithDefault(ID string) *Character {
	char := m.GetCharacter(ID)
	if char != nil {
		return char
	}
	char = m.GetDefault()
	if char != nil {
		return char
	}

	return nil
}

// GetCharacters はJSONバイト列からキャラクターマップをパースして返します。
func GetCharacters(charactersJSON []byte) (CharactersMap, error) {
	var charsMap CharactersMap
	if err := json.Unmarshal(charactersJSON, &charsMap); err != nil {
		return nil, fmt.Errorf("キャラクター情報のJSONパースに失敗しました: %w", err)
	}
	return charsMap, nil
}

// LoadCharacterMap は指定されたパスからキャラクター設定を読み込みます。
func LoadCharacterMap(ctx context.Context, reader ContentReader, path string) (CharactersMap, error) {
	if path == "" {
		return nil, fmt.Errorf("キャラクター設定のパスが空です")
	}

	rc, err := reader.Open(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("キャラクター設定ファイルを開けませんでした (path: %s): %w", path, err)
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("キャラクター設定ファイルの読み込みに失敗しました (path: %s): %w", path, err)
	}

	charsMap, err := GetCharacters(data)
	if err != nil {
		return nil, fmt.Errorf("キャラクター設定の解析に失敗しました (path: %s): %w", path, err)
	}

	return charsMap, nil
}

// GetSeedFromString は文字列から決定論的なシード値を生成します。
func GetSeedFromString(s string) int64 {
	if s == "" {
		return 0
	}
	hash := sha256.Sum256([]byte(s))
	return int64(binary.BigEndian.Uint64(hash[:8]))
}
