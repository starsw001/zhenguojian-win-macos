package core

import (
	"strconv"
	"strings"
	"testing"
)

// dsdTestAaChar 返回DSDTestAaChar。
func dsdTestAaChar(octal string) string {
	parts := make([]string, 0, len(octal))
	for _, digit := range octal {
		switch digit {
		case '0':
			parts = append(parts, "(c^_^o)")
		case '1':
			parts = append(parts, "(ﾟΘﾟ)")
		case '3':
			parts = append(parts, "(o^_^o)")
		case '4':
			parts = append(parts, "(ﾟｰﾟ)")
		default:
			parts = append(parts, string(digit))
		}
	}
	return "(ﾟДﾟ)[ﾟεﾟ]+" + strings.Join(parts, "+")
}

// TestDSDAaencodeDecoderExtractsSignedVPath 验证「DSD Aaencode Decoder Extracts Signed V Path」回归场景。
func TestDSDAaencodeDecoderExtractsSignedVPath(t *testing.T) {
	payload := `var vPath = "/video/fixture/1000k/index.m3u8?sign=abc%2Fdef";`
	encoded := "(ﾟДﾟ) ['_'] ( (ﾟДﾟ) ['_']"
	for _, char := range payload {
		encoded += dsdTestAaChar(strconv.FormatInt(int64(char), 8))
	}
	encoded += "+ (ﾟДﾟ)[ﾟoﾟ]"
	page := `<html><script type="text/javascript">` + encoded + `</script></html>`
	path, ok := dsdSignedPathFromVplayer(page)
	if !ok || path != "/video/fixture/1000k/index.m3u8?sign=abc%2Fdef" {
		t.Fatalf("signed vPath not decoded: %q %v", path, ok)
	}
}

// TestDSDVplayerMediaPathDropsOrigin 验证「DSD Vplayer Media Path Drops Origin」回归场景。
func TestDSDVplayerMediaPathDropsOrigin(t *testing.T) {
	got := dsdVplayerMediaPath("https://www.dsd.com.se/video/a/1000k/index.m3u8?token=a%2Fb")
	if got != "/video/a/1000k/index.m3u8?token=a%2Fb" {
		t.Fatalf("wrong media path: %s", got)
	}
	if dsdVplayerMediaPath("/video/a/index.m3u8") != "/video/a/index.m3u8" {
		t.Fatal("relative media path changed")
	}
}
