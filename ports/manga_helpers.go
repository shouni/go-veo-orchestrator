package ports

import "sort"

// UniqueSpeakerIDs はパネルのスライスから重複しない SpeakerID を抽出します。
func (ps Panels) UniqueSpeakerIDs() []string {
	set := make(map[string]struct{})
	for _, panel := range ps {
		if panel.SpeakerID != "" {
			set[panel.SpeakerID] = struct{}{}
		}
	}

	uniqueIDs := make([]string, 0, len(set))
	for id := range set {
		uniqueIDs = append(uniqueIDs, id)
	}
	sort.Strings(uniqueIDs)

	return uniqueIDs
}
