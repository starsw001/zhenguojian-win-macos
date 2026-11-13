package core

import (
	"encoding/binary"
	"testing"
)

// TestAVIFBrandDetectionKeepsHEICSeparate 验证「AVIF Brand Detection Keeps HEIC Separate」回归场景。
func TestAVIFBrandDetectionKeepsHEICSeparate(t *testing.T) {
	for _, brand := range []string{"avif", "avis", "mif1"} {
		data := make([]byte, 24)
		binary.BigEndian.PutUint32(data, 24)
		copy(data[4:], "ftyp")
		copy(data[8:], brand)
		copy(data[16:], "avif")
		if !isAVIFImage(data) || !nativeIsCoverImage(data) || isHEICImage(data) {
			t.Fatalf("AVIF was rejected or treated as HEVC: %q", brand)
		}
		binary.BigEndian.PutUint32(data, 4096)
		if isAVIFImage(data) {
			t.Fatal("truncated container accepted as AVIF")
		}
	}
	if isAVIFImage([]byte("not an AVIF image")) {
		t.Fatal("non-image data accepted as AVIF")
	}
}
