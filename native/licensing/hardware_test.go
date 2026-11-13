package licensing

import (
	"encoding/binary"
	"testing"
)

// TestSMBIOSUUIDBoundsAndStableField 验证「SMBIOSUUID Bounds And Stable Field」回归场景。
func TestSMBIOSUUIDBoundsAndStableField(t *testing.T) {
	data := make([]byte, 8+25+2)
	binary.LittleEndian.PutUint32(data[4:8], 27)
	data[8], data[9] = 1, 25
	for i := 0; i < 16; i++ {
		data[16+i] = byte(i + 1)
	}
	uuid, err := smbiosUUID(data)
	if err != nil || len(uuid) != 16 || uuid[15] != 16 {
		t.Fatal("valid UUID rejected")
	}
	data[32] = 5
	other, err := smbiosUUID(data)
	if err != nil || string(other) != string(uuid) {
		t.Fatal("wake-up type changed device identity")
	}
	for _, malformed := range [][]byte{nil, data[:12], append([]byte(nil), make([]byte, 40)...)} {
		if _, err := smbiosUUID(malformed); err == nil {
			t.Fatal("malformed table accepted")
		}
	}
	for i := 16; i < 32; i++ {
		data[i] = 0
	}
	if _, err := smbiosUUID(data); err == nil {
		t.Fatal("unassigned UUID accepted")
	}
	for i := 16; i < 32; i++ {
		data[i] = 255
	}
	if _, err := smbiosUUID(data); err == nil {
		t.Fatal("all-FF UUID accepted")
	}
}
