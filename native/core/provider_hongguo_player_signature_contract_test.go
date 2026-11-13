package core

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// TestHongguoPlayerSignatureContract148 验证「Hongguo Player Signature Contract 148」回归场景。
func TestHongguoPlayerSignatureContract148(t *testing.T) {
	query := "_rticket=1700000000000&ac=wifi&aid=8662&app_name=novelread&channel=update_64&device_brand=Redmi&device_id=1234567890123456&device_platform=android&device_type=25053RT47C&dpi=520&iid=9876543210123456&language=zh&manifest_version_code=73532&os=android&os_api=36&os_version=16&resolution=1280%2A2772&ssmix=a&update_version_code=73532&version_code=73532&version_name=7.3.5.32"
	body := []byte("{\"biz_param\":{\"device_level\":1},\"mixed_video_id_map\":{\"1004\":[\"5678\"]}}")
	nonce := hongguoPlayerNonce{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
	hash := func(s string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(s))) }
	hashes := map[string]string{
		"argus":     hash(hongguoPlayerArgus(query, body, 1700000000, "9876543210123456", nonce)),
		"nilBody":   hash(hongguoPlayerArgus(query, nil, 1700000000, "9876543210123456", nonce)),
		"emptyBody": hash(hongguoPlayerArgus(query, []byte{}, 1700000000, "9876543210123456", nonce)),
		"ladon":     hash(hongguoPlayerLadon("8662", 1700000000, nonce[2:6])),
	}
	want := map[string]string{"argus": "b4a86aa03d093aa2b41194603b314da635bd4374393cfe6a77d3f3c7ff0258c5", "nilBody": "08698a59b5ccb287f5ce4fb428a695bfa91fff5d749cb6b6d3e30805bc8c69ed", "emptyBody": "2b4d5df67b68f2a3ae79a416ac3650021c88b8e281d3d05e18802b84863a39e9", "ladon": "8e5727583445e322e11a46e82abcff0e1c8962a4eb51553a5c18e3b15358a06c"}
	matched := map[string]bool{}
	for key, got := range hashes {
		matched[key] = got == want[key]
	}
	b, _ := json.Marshal(map[string]any{"synthetic": true, "matches": matched, "hashes": hashes})
	fmt.Printf("HG148_FIXTURE=%s\n", b)
	old := os.Getenv("HG148_EXPECT") == "original"
	for name, ok := range matched {
		if ok == old {
			t.Errorf("%s unexpected signature contract match: %t", name, ok)
		}
	}
}
