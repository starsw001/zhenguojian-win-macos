package core

import "encoding/binary"

// isAVIFImage 判断是否为AVIF图片。
func isAVIFImage(data []byte) bool {
	if len(data) < 16 || string(data[4:8]) != "ftyp" {
		return false
	}
	size := int(binary.BigEndian.Uint32(data))
	if size < 16 || size > 256 || size > len(data) {
		return false
	}
	for offset := 8; offset+4 <= size; offset += 4 {
		if offset != 12 && (string(data[offset:offset+4]) == "avif" || string(data[offset:offset+4]) == "avis") {
			return true
		}
	}
	return false
}
