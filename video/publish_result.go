package video

// PublishResult holds the files produced by a publish.
type PublishResult struct {
	MetadataPath string   // path of the generated video_music_meta.json
	ImagePaths   []string // paths of every saved keyframe image
}
