package runner

import (
	"path"

	"github.com/shouni/go-remote-io/remoteio"
)

const (
	defaultImageDir      = "images"
	defaultKeyframeName  = "keyframe.png"
	defaultVideoMetaJSON = "video_music_meta.json"
)

func defaultKeyframePath() string {
	return path.Join(defaultImageDir, defaultKeyframeName)
}

func resolveOutputPath(baseDir, fileName string) (string, error) {
	return remoteio.Join(baseDir, fileName), nil
}

func resolveBaseURL(rawPath string) string {
	return remoteio.Dir(rawPath)
}

func generateIndexedPath(basePath string, index int) (string, error) {
	return remoteio.IndexedPath(basePath, index)
}
