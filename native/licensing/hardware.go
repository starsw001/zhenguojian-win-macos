package licensing

import (
	"encoding/binary"
	"errors"
)

// smbiosUUID 从 SMBIOS 数据中提取系统 UUID。
func smbiosUUID(buffer []byte) ([]byte, error) {
	invalid := errors.New("本机缺少可靠的 SMBIOS UUID，无法完成单机授权，请联系管理员")
	if len(buffer) < 8 {
		return nil, invalid
	}
	length := int(binary.LittleEndian.Uint32(buffer[4:8]))
	if length > len(buffer)-8 || length < 4 {
		return nil, invalid
	}
	data := buffer[8 : 8+length]
	for len(data) >= 4 {
		kind, size := data[0], int(data[1])
		if size < 4 || size > len(data) {
			return nil, invalid
		}
		if kind == 1 && size >= 24 {
			uuid := data[8:24]
			zero, empty := true, true
			for _, value := range uuid {
				zero = zero && value == 0
				empty = empty && value == 255
			}
			if zero || empty {
				return nil, invalid
			}
			return append([]byte(nil), uuid...), nil
		}
		if kind == 127 {
			break
		}
		next := size
		for next+1 < len(data) && (data[next] != 0 || data[next+1] != 0) {
			next++
		}
		if next+1 >= len(data) {
			return nil, invalid
		}
		data = data[next+2:]
	}
	return nil, invalid
}
