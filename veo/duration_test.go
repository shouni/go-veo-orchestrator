package veo

import (
	"reflect"
	"testing"
)

// TestDurationsForMode verifies each Veo generation mode reports the durations it actually
// accepts: reference_to_video is 8s only, video_extension is 7s only, and the image-input
// modes share {4,6,8}. An unknown mode falls back to the image_to_video set rather than
// reporting "no supported duration", which would make every cut fail validation.
func TestDurationsForMode(t *testing.T) {
	tests := []struct {
		mode GenerationMode
		want []float64
	}{
		{ModeImageToVideo, []float64{4, 6, 8}},
		{ModeFramesToVideo, []float64{4, 6, 8}},
		{ModeReferenceToVideo, []float64{8}},
		{ModeVideoExtension, []float64{7}},
		{GenerationMode("unknown"), []float64{4, 6, 8}},
	}
	for _, tt := range tests {
		if got := DurationsForMode(tt.mode); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("DurationsForMode(%s) = %v, want %v", tt.mode, got, tt.want)
		}
	}
}

// TestDurationAccessorsReturnCopies verifies the exported duration lists cannot be mutated
// through a caller's copy, so one consumer editing the slice it received can never change
// what every other consumer sees.
func TestDurationAccessorsReturnCopies(t *testing.T) {
	got := ImageToVideoDurationsSec()
	got[0] = 99
	if fresh := ImageToVideoDurationsSec(); fresh[0] != 4 {
		t.Fatalf("ImageToVideoDurationsSec()[0] = %v after caller mutation, want 4", fresh[0])
	}
	refs := ReferenceToVideoDurationsSec()
	refs[0] = 99
	if fresh := ReferenceToVideoDurationsSec(); fresh[0] != 8 {
		t.Fatalf("ReferenceToVideoDurationsSec()[0] = %v after caller mutation, want 8", fresh[0])
	}
}

func TestIsSupportedDuration(t *testing.T) {
	tests := []struct {
		duration float64
		mode     GenerationMode
		want     bool
	}{
		{8, ModeImageToVideo, true},
		{4, ModeImageToVideo, true},
		{5, ModeImageToVideo, false},
		{7, ModeImageToVideo, false},
		{8, ModeReferenceToVideo, true},
		{6, ModeReferenceToVideo, false},
		{7, ModeVideoExtension, true},
		{8, ModeVideoExtension, false},
		// 誤差拡散などで生じる微小なズレは同値として扱う。
		{8.0000000001, ModeImageToVideo, true},
		{0, ModeImageToVideo, false},
	}
	for _, tt := range tests {
		if got := IsSupportedDuration(tt.duration, tt.mode); got != tt.want {
			t.Errorf("IsSupportedDuration(%v, %s) = %v, want %v", tt.duration, tt.mode, got, tt.want)
		}
	}
}

// TestSnapDuration verifies rounding to the nearest supported duration, with ties going to the
// longer candidate (cutting a scene short is more visible than letting it run long).
func TestSnapDuration(t *testing.T) {
	candidates := ImageToVideoDurationsSec()
	tests := []struct {
		duration float64
		want     float64
	}{
		{3, 4},
		{4.4, 4},
		{5, 6}, // 同距離 → 長い方
		{6.9, 6},
		{7.5, 8},
		{20, 8},
	}
	for _, tt := range tests {
		if got := SnapDuration(tt.duration, candidates); got != tt.want {
			t.Errorf("SnapDuration(%v) = %v, want %v", tt.duration, got, tt.want)
		}
	}
	// 候補が空なら丸めようがないので、入力をそのまま返す。
	if got := SnapDuration(5, nil); got != 5 {
		t.Errorf("SnapDuration(5, nil) = %v, want 5", got)
	}
}

// TestChainDurations verifies the achievable total lengths of one continuation chain
// (base cut + 7s video_extension cuts) stop before the continuation cap, and that a
// reference_to_video base ({8}) yields the 8s-based subset of the image_to_video result.
func TestChainDurations(t *testing.T) {
	got := ChainDurations(ImageToVideoDurationsSec())
	want := []float64{4, 6, 8, 11, 13, 15, 18, 20, 22}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ChainDurations(image_to_video) = %v, want %v", got, want)
	}
	if longest := got[len(got)-1]; longest+VideoExtensionDurationSec <= ContinuationMaxDurationSec {
		t.Errorf("longest chain %v could still be extended without reaching the cap %v", longest, ContinuationMaxDurationSec)
	}

	got = ChainDurations(ReferenceToVideoDurationsSec())
	want = []float64{8, 15, 22}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ChainDurations(reference_to_video) = %v, want %v", got, want)
	}

	if got := ChainDurations(nil); len(got) != 0 {
		t.Errorf("ChainDurations(nil) = %v, want empty", got)
	}
}
