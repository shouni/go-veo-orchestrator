package ports

import "sort"

// UniqueCharacterIDs はカットのスライスから重複しない CharacterID を抽出します。
func (cs Cuts) UniqueCharacterIDs() []string {
	set := make(map[string]struct{})
	for _, cut := range cs {
		id := cut.CharacterID
		if id == "" {
			id = cut.SpeakerID
		}
		if id != "" {
			set[id] = struct{}{}
		}
	}

	uniqueIDs := make([]string, 0, len(set))
	for id := range set {
		uniqueIDs = append(uniqueIDs, id)
	}
	sort.Strings(uniqueIDs)

	return uniqueIDs
}

// UniqueSpeakerIDs は旧 API 互換のヘルパーです。
func (cs Cuts) UniqueSpeakerIDs() []string {
	return cs.UniqueCharacterIDs()
}
