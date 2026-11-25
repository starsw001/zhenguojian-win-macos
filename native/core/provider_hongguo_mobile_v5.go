package core

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const hongguoMobileV5API = "https://api5-normal-sinfonlinea.fqnovel.com/novel/player/multi_video_model/player/v1"
const hongguoMobileV5TabAPI = "https://api5-normal-sinfonlineb.fqnovel.com/reading/bookapi/video_tab/multi_video_model/v1/"
const hongguoMobileV5UserAgent = "com.phoenix.read/73932 (Linux; U; Android 12; zh_CN_#Hans; PGT-AN10; Build/V417IR;tt-ok/3.12.13.20)"
const hongguoMobileV5License = "646637794"

var hongguoMobileV5Magic = mustHex("2d4b4fca49750d433fb5ae2c226dcc56")
var hongguoMobileV5SignKey = mustHex("b23c2a470a20d105dc3f8c4438f8272ed46d6c89ffdcdedd5108698d26e9489d")
var hongguoMobileV5SBox = mustHex("53290fe9e51f316f90f74a7e034d367784ab49237cd0135cdc632c322fd3be0e2624a417ecd9151a7ff64f60730a87f9445ec6ada2f865d22b696c4188f24c0df37b75510b939f6bc2fe6883e68bc8c722181ba0bdb08c9871b50c9a16b46eea8eed20ba4b8a14e0fcf1468dbbef05a93e5a9d7dcf06898635c1fd99f5de287a80c594daaab19b6279fbd75070cb52eb2555c456e39e7839853874e2f0013ac0b64742d8ca10e4b79c955de8b843ce4ed464a6a72ed508091c92e7b3eedd1e1dd12afac321618fa3d65f9167c982f43d66a53c6a1997585b346dbf9672ff37cdbc073b2dae11453304df00815476b2a13fccdb30b957a8acafe127400248591238922563e36406d37a24100b7902a028044e218453c2a399199c9d13dc40c55ba67e502273850ebdc1a2e2188b8f2d9e3cb909b8c46a2a9f4a6dd93007eafa65b7b3a881aa908cc74f0ad76bbbb58617c37012ca05a469fd44e62eecf1e4272ce860c0ae5eadf99b20d8f0234174006f3f110d7657d01645d4eff8f71471cbfe1cb2f52648727bf3c9de55153e031b4d61757db63dbf7782eeafe9018a8891362fb14759ac32bab03b349aeb5a377c5156be4c8dfbe13a684bda39cf836697c894545fe0f2491acea9fc6252f4ff1e0fe72bd2df6e67a7f635e5bcc6d1a5dd951d89960887a18eb40c785cdb1f80ed42299333ab98cdd67f316cd54346585dccd759fb722789b08807cc0fda293f7e5e1819bb0353517020b31d3d00374952bea2c421e862040a9ec892a05b266b7a0d0cc316f483b81af39715657ffe32ad8de0c13077646035236ee5363e5f900e9a7cf850a81cd9960b6df73317226f99eaa6fff15cec34acb98246639b918ab2851466a7cdfc4ad1b52c2ae95a58d3734e47ddd5b7560193f644ef9c408b7dee39b4d6aabf4de761e3252d4c7ba3843187745d55e6a5db05d42b45caed43c0c5d0b64bfa9d94a4a9d8f2f0e1a108dcfd288fd2bc1fc7bd675412138169cb09249875ba3b68cfb11180afae104fc9789571c6dfce02413886796ae21e3c488cc21b9f423a066c2eabf98e2fdeeb5776e4f5d09a515ba58cbbab3e37b09fd4f6d9244df80512e84cd7956be301cf64f1429d58577cc241f3a2c655144f81aaf95a4e108da894b38970561ae9002619fbbac91fa922ed202bfc62cb08e0de2a7f3631435f8b47c8a3c563b4db4071ce74c46da6fa696a9e282d1e825d663b93bfd56c48dce1884bb10fccea063929f746443fd830a4232e53da21a1f2d1bdfebc3a02c10a1b8ad31d04fd616590d272ac737e98eb33af60528716bee78ec0ee8699031177277d9cff32542c67caf5f44591681335ae0e178f3409a7803db83892dde5b9b7f0c750e24aef077badd678a09b850b84ec1597e4963cb5e675df1c0d6fb618496ecd76c35c798325590c2fb27a5e")

type hongguoMobileV5Aes struct {
	box   []byte
	con   [4]int
	order [16]int
	keys  [12][4]byte
}

func newHongguoMobileV5Aes(key []byte, timestamp uint32) *hongguoMobileV5Aes {
	word := int(timestamp & 3)
	conSets := [4][4]int{{2, 0, 3, 1}, {1, 3, 2, 0}, {3, 2, 1, 0}, {2, 3, 1, 0}}
	orderSets := [4][16]int{
		{0, 9, 14, 15, 4, 13, 2, 7, 8, 1, 6, 3, 12, 5, 10, 11},
		{0, 9, 14, 11, 4, 13, 2, 3, 8, 1, 6, 15, 12, 5, 10, 7},
		{0, 9, 14, 3, 4, 13, 2, 15, 8, 1, 6, 11, 12, 5, 10, 7},
		{0, 9, 14, 15, 4, 13, 2, 3, 8, 1, 6, 11, 12, 5, 10, 7},
	}
	initials := [4]uint32{0xbbb5de72, 0xd4638d5b, 0x92a5f73b, 0x89f8a1c9}
	value := &hongguoMobileV5Aes{
		box:   hongguoMobileV5SBox[word*256 : (word+1)*256],
		con:   conSets[word],
		order: orderSets[word],
	}
	initial := make([]byte, 16)
	for i := 0; i < 4; i++ {
		binary.LittleEndian.PutUint32(initial[i*4:], initials[word])
	}
	expanded := make([]byte, 48)
	copy(expanded, hongguoXorBytes(initial, key))
	rounds := 8
	for i := 4; i < 12; i++ {
		at := 4 * (i - 1)
		a, b, c, d := expanded[at], expanded[at+1], expanded[at+2], expanded[at+3]
		if i&3 == 0 {
			a, b, c, d = byte((initials[word]>>uint(rounds&24))^uint32(value.box[b])), value.box[c], value.box[d], value.box[a]
		}
		rounds += 2
		expanded[at+4] = a ^ expanded[at-12]
		expanded[at+5] = b ^ expanded[at-11]
		expanded[at+6] = c ^ expanded[at-10]
		expanded[at+7] = d ^ expanded[at-9]
	}
	for i := range value.keys {
		copy(value.keys[i][:], expanded[i*4:i*4+4])
	}
	return value
}

func (value *hongguoMobileV5Aes) block(input []byte) []byte {
	var rows [4][4]byte
	for i := 0; i < 4; i++ {
		copy(rows[i][:], input[i*4:i*4+4])
	}
	add := func(offset int) {
		for i := 0; i < 4; i++ {
			for j := 0; j < 4; j++ {
				rows[i][j] ^= value.keys[offset+i][value.con[j]]
			}
		}
	}
	add(0)
	for round := 1; round <= 2; round++ {
		for i := 0; i < 4; i++ {
			for j := 0; j < 4; j++ {
				rows[i][j] = value.box[rows[i][j]]
			}
		}
		before := rows
		for i := 0; i < 4; i++ {
			rows[i] = before[value.con[i]]
		}
		var flat [16]byte
		for i := 0; i < 4; i++ {
			copy(flat[i*4:], rows[i][:])
		}
		for i := 0; i < 16; i++ {
			rows[i/4][i%4] = flat[value.order[i]]
		}
		if round == 1 {
			before = rows
			for i := 0; i < 4; i++ {
				for j := 0; j < 4; j++ {
					rows[i][j] = before[i][value.con[j]]
				}
			}
			hongguoAesMix(&rows)
		}
		add(round * 4)
	}
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			rows[i][j] ^= value.keys[4+i][j]
		}
	}
	output := make([]byte, 16)
	for i := 0; i < 4; i++ {
		copy(output[i*4:], rows[i][:])
	}
	return output
}

func (value *hongguoMobileV5Aes) encrypt(source, iv []byte) []byte {
	if len(source) < 248 || len(iv) != 16 {
		return nil
	}
	wire := [8]int{6, 5, 3, 4, 2, 7, 1, 0}
	compact := [8]int{1, 7, 3, 6, 2, 0, 4, 5}
	plain := make([]byte, 32)
	for i := 0; i < 31; i++ {
		for j := 0; j < 8; j++ {
			plain[i] |= ((source[i*8+j] >> wire[j]) & 1) << compact[j]
		}
	}
	plain[31] = 1
	first := value.block(hongguoXorBytes(plain[:16], iv))
	key := append(first, value.block(hongguoXorBytes(plain[16:], first))...)
	output := append([]byte(nil), source...)
	for i := 0; i < 31; i++ {
		for j := 0; j < 8; j++ {
			at := i*8 + j
			output[at] = output[at]&^(1<<wire[j]) | ((key[i]>>compact[j])&1)<<wire[j]
		}
	}
	return append([]byte{key[len(key)-1]}, output...)
}

var hongguoMobileV5HashTable = [64]uint32{
	0xebb64faf, 0x07aadcc2, 0xcf3187bf, 0xe01138ff, 0x6d0bfcff, 0x5a30a3be, 0xb41ad638, 0x34180eb8,
	0xf233eb6f, 0xb1a584cc, 0xccc30dc7, 0x47d1db51, 0xd55653de, 0x70a84fa1, 0x57473c12, 0xf76f0288,
	0x2c077f0a, 0xda0dcad0, 0xfbb86f6c, 0xfdc4cf00, 0x688a020d, 0xe676c6a6, 0x8cd6338b, 0x1a3c8d0e,
	0xcce8b06b, 0x6ad0ed0b, 0xa0522717, 0xdc71ac83, 0x2285db71, 0xd5b4dda6, 0x736f8650, 0x6560306c,
	0x617ce2a6, 0xe423417e, 0x0a40e143, 0x544e4032, 0x88dffb2a, 0x716c1ae0, 0x4c467a88, 0x05b23bb3,
	0xe1d0b866, 0xbaa3dcb8, 0xae3374d3, 0xc3381a50, 0x1702f75b, 0xfe6da368, 0xf0b4cf48, 0x4e0ffbb8,
	0x72aad10d, 0x26c53a3d, 0xf2bce0f6, 0xb4557581, 0x4a257fdd, 0x8c3182a2, 0xab0b3b86, 0x3d5dfb14,
	0x4f103634, 0xd37b52d7, 0x444eff16, 0xeb0a33d1, 0x6ca86f6e, 0x00284ba7, 0x08387cfa, 0x5fb37586,
}

func hongguoMobileV5Branch(query string, body []byte, timestamp uint32) int {
	queryDigest := hongguoMultiSM3([]byte(query))
	bodyDigest := [16]byte{}
	if len(body) > 0 {
		bodyDigest = md5.Sum(body)
	}
	value := hongguoGetIv(hongguoGetIv(hongguoGetIv(0x20230928, queryDigest), bodyDigest[:]), hongguoLe32(timestamp))
	return int(value & 15)
}

func hongguoMobileV5HashF13(queryDigest, bodyDigest []byte, timestamp uint32) []byte {
	count := uint32(0x9d4a97c2) + timestamp
	table := make([]uint32, len(hongguoMobileV5HashTable))
	for i, current := range hongguoMobileV5HashTable {
		table[i] = ror32(current, int(count+1))
	}
	shift := (count + 2) & 7
	padSeed := [4]byte{0xfa, 0x45, 0x61, 0xd7}
	pad := make([]byte, 4)
	for i, current := range padSeed {
		pad[i] = byte((uint32(current) | uint32(current)<<8) >> shift)
	}
	data := append(append(append(append(append([]byte(nil), queryDigest...), bodyDigest...), hongguoLe32(timestamp)...), pad...), mustHex("00000000000001a0")...)
	words := make([]uint32, 16, 128)
	for i := range words {
		words[i] = binary.BigEndian.Uint32(data[i*4:])
	}
	for i := 0; i < 112; i++ {
		x, y := words[i+1], words[i+14]
		words = append(words, words[i]+words[i+9]+(rol32(x, 14)^rol32(x, 25)^(x>>3))+(rol32(y, 13)^rol32(y, 15)^(y>>10)))
	}
	initialValues := [8]uint32{0x7aba4fc8, 0x67166507, 0x6403fa00, 0x340f512f, 984304912, 3005047866, 2874125293, 2152413264}
	state := make([]uint32, 8)
	initial := make([]uint32, 8)
	for i, current := range initialValues {
		initial[i] = ror32(current, int(count))
		state[i] = initial[i]
	}
	chosen := [11]int{96, 1, 3, 4, 0, 6, 5, 2, 0, 7, 7}
	for i := 0; i < chosen[0]; i++ {
		base := uint32(0x9d4a97c2) + uint32(i)
		n1 := ((state[chosen[3]] ^ state[chosen[4]]) & state[chosen[1]]) ^ state[chosen[3]]
		n2 := rol32(state[chosen[1]], 26) ^ rol32(state[chosen[1]], 21) ^ rol32(state[chosen[1]], 7)
		n4 := words[int(base&127)] + n1 + n2 + table[int(base&63)] + state[chosen[5]]
		n5 := rol32(state[chosen[2]], 30) ^ rol32(state[chosen[2]], 19) ^ rol32(state[chosen[2]], 10)
		n6 := (state[chosen[2]] & state[chosen[6]]) | ((state[chosen[2]] | state[chosen[6]]) & state[chosen[7]])
		old := state[chosen[9]]
		last := state[len(state)-1]
		copy(state[1:], state[:len(state)-1])
		state[0] = last
		state[chosen[10]] = n5 + n6 + n4
		state[chosen[8]] = old + n4
	}
	result := make([]byte, 32)
	for i := 0; i < 8; i++ {
		binary.BigEndian.PutUint32(result[i*4:], state[i]+initial[i])
	}
	for i := 0; i < 16; i++ {
		result[i] ^= result[i+16]
	}
	folded := result[:16:16]
	return append(folded, hongguoLe32(hongguoSumMd5(folded)|0x20)...)
}

func hongguoMobileV5Helios(timestamp uint32) string {
	random := hongguoMultiRandomUint32()
	digest := md5.Sum(append(hongguoLe32(random), []byte("8662")...))
	ascii := []byte(hex.EncodeToString(digest[:]))
	words := make([]uint64, 4)
	for i := range words {
		words[i] = binary.LittleEndian.Uint64(ascii[i*8:])
	}
	table := []uint64{words[0]}
	left, right := words[0], words[1]
	queue := append([]uint64(nil), words[2:]...)
	for i := 0; i < 34; i++ {
		value := (ror64(right, 8) + left) ^ uint64(i)
		queue = append(queue, value)
		value ^= ror64(left, 61)
		table = append(table, value)
		left, right, queue = value, queue[0], queue[1:]
	}
	plain := []byte(fmt.Sprintf("%d-%s-8662", timestamp, hongguoMobileV5License))
	padding := 16 - len(plain)%16
	plain = append(plain, bytes.Repeat([]byte{byte(padding)}, padding)...)
	output := append([]byte(nil), hongguoLe32(random)...)
	for offset := 0; offset < len(plain); offset += 16 {
		left, right := binary.LittleEndian.Uint64(plain[offset:]), binary.LittleEndian.Uint64(plain[offset+8:])
		for i := 0; i < 34; i++ {
			right = table[i] ^ (left + ror64(right, 8))
			left = right ^ ror64(left, 61)
		}
		block := make([]byte, 16)
		binary.LittleEndian.PutUint64(block, left)
		binary.LittleEndian.PutUint64(block[8:], right)
		output = append(output, block...)
	}
	return base64.StdEncoding.EncodeToString(output)
}

func hongguoMobileV5Medusa(fullURL string, body []byte, timestamp uint32, values map[string]string) string {
	query := fullURL
	if offset := strings.IndexByte(fullURL, '?'); offset >= 0 {
		query = fullURL[offset+1:]
	}
	queryDigest := hongguoMultiSM3([]byte(query))
	bodyDigest := [16]byte{}
	if len(body) > 0 {
		bodyDigest = md5.Sum(body)
	}
	f13 := hongguoMobileV5HashF13(queryDigest, bodyDigest[:], timestamp)
	flags := make([]byte, 8)
	deviceInfo := hongguoProto(
		hongguoProtoFieldSint(1, 1), hongguoProtoFieldSint(2, 2), hongguoProtoFieldString(3, "8662"),
		hongguoProtoFieldString(4, values["device_id"]), hongguoProtoFieldString(5, hongguoMultiProcessNonce),
		hongguoProtoFieldString(6, "!noperm!"), hongguoProtoFieldString(12, "Asia/Shanghai,8"),
		hongguoProtoFieldString(13, "zh_CN"), hongguoProtoFieldString(22, values["os_version"]),
		hongguoProtoFieldSint(25, hongguoMultiProcessStarted), hongguoProtoFieldString(30, values["device_type"]),
		hongguoProtoFieldString(31, "HONOR"), hongguoProtoFieldString(32, values["device_type"]),
		hongguoProtoFieldString(34, "HONOR"),
	)
	environment := hongguoProto(
		hongguoProtoFieldString(6, "v04.10.03.04-bugfix"), hongguoProtoFieldString(8, values["channel"]),
		hongguoProtoFieldBytes(12, deviceInfo), hongguoProtoFieldString(14, values["version_name"]),
	)
	messageRandom := hongguoMultiRandomUint32()
	message := hongguoProto(
		hongguoProtoFieldBytes(1, hongguoMobileV5Magic), hongguoProtoFieldSint(2, 5),
		hongguoProtoFieldSint(3, int64(messageRandom)), hongguoProtoFieldString(4, "8662"),
		hongguoProtoFieldString(5, values["device_id"]), hongguoProtoFieldString(6, hongguoMobileV5License),
		hongguoProtoFieldString(7, values["version_name"]), hongguoProtoFieldString(8, "v04.10.03-ml-android"),
		hongguoProtoFieldSint(9, 0x040a0300), hongguoProtoFieldBytes(10, flags),
		hongguoProtoFieldSint(12, int64(timestamp)), hongguoProtoFieldBytes(13, f13),
		hongguoProtoFieldBytes(14, queryDigest[:6]),
		hongguoProtoFieldBytes(15, hongguoProto(hongguoProtoFieldSint(1, 1), hongguoProtoFieldSint(2, 1), hongguoProtoFieldSint(3, 1), hongguoProtoFieldSint(5, 1))),
		hongguoProtoFieldSint(17, int64(timestamp)), hongguoProtoFieldString(20, "none"),
		hongguoProtoFieldSint(21, 312), hongguoProtoFieldBytes(23, environment),
		hongguoProtoFieldString(24, `{"cmr":16777216,"cmr2":16777216,"un_h":11,"vpn":0,"kd":0,"dyn":"","do":1,"tk":true}`),
	)
	nonce := hongguoMultiRandomUint32()
	hash, seed := hongguoKeyHash(hongguoMobileV5SignKey, nonce)
	transformed := append(append([]byte(nil), flags...), hongguoXmxor(message, hash)...)
	for left, right := 0, len(transformed)-1; left < right; left, right = left+1, right-1 {
		transformed[left], transformed[right] = transformed[right], transformed[left]
	}
	for i := range transformed {
		transformed[i] ^= seed[3-i%4]
	}
	check := uint32(0x18000001) | uint32(queryDigest[0]&63)<<14 | uint32(f13[0]&63)<<8
	packed := append([]byte{0xca}, hongguoLe32(hongguoMultiRandomUint32())...)
	packed = append(packed, hongguoLe32(check)...)
	packed = append(packed, transformed...)
	packed = append(packed, byte(nonce>>16), byte(nonce>>24))
	leftKey, rightKey := md5.Sum(hongguoMobileV5SignKey[:16]), md5.Sum(hongguoMobileV5SignKey[16:])
	encrypted := newHongguoMobileV5Aes(leftKey[:], timestamp).encrypt(packed, rightKey[:])
	prefix := append(hongguoLe32(5), hongguoMobileV5Magic...)
	timestampBytes := hongguoLe32(timestamp)
	for i := range prefix {
		prefix[i] ^= timestampBytes[i&3]
	}
	output := append(prefix, byte(nonce), byte(nonce>>8), 0, 1)
	output = append(output, encrypted...)
	return base64.StdEncoding.EncodeToString(output)
}

func hongguoMobileV5Payload(videoID string) ([]byte, error) {
	plain, err := json.Marshal(map[string]any{
		"biz_param": map[string]any{
			"detail_page_version": 0, "device_level": 1, "disable_digg_stat": false,
			"disable_video_relate_book": false, "need_all_video_definition": true,
			"need_mp4_align": false, "use_os_player": false, "use_server_dns": false,
			"video_platform": 1024,
		},
		"mixed_video_id_map": map[string]any{"1004": []string{videoID}},
		"dr_scene":           "player",
	})
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	writer := gzip.NewWriter(&output)
	if _, err = writer.Write(plain); err != nil {
		return nil, err
	}
	if err = writer.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func hongguoMobileV5SignedRequestFor(baseURL string, pairs [][2]string, body []byte, now time.Time) (string, map[string]string, error) {
	timestamp := uint32(now.Unix())
	var query string
	for offset := int64(0); offset <= 1023; offset++ {
		candidate := append(append([][2]string(nil), pairs...), [2]string{"_rticket", strconv.FormatInt(now.UnixMilli()+offset, 10)})
		query = hongguoPythonUrlEncode(candidate)
		if hongguoMobileV5Branch(query, body, timestamp) == 12 {
			values := make(map[string]string, len(candidate))
			for _, pair := range candidate {
				values[pair[0]] = pair[1]
			}
			fullURL := baseURL + "?" + query
			headers := map[string]string{
				"X-Khronos": strconv.FormatUint(uint64(timestamp), 10),
				"X-Helios":  hongguoMobileV5Helios(timestamp),
			}
			if len(body) > 0 {
				stub := md5.Sum(body)
				headers["Content-Type"] = "application/json; charset=utf-8"
				headers["X-SS-STUB"] = strings.ToUpper(hex.EncodeToString(stub[:]))
			}
			headers["X-Medusa"] = hongguoMobileV5Medusa(fullURL, body, timestamp, values)
			return fullURL, headers, nil
		}
	}
	return "", nil, errors.New("红果新版 App 播放请求签名失败")
}

func hongguoMobileV5SignedRequest(device hongguoPlaybackDevice, body []byte, now time.Time) (string, map[string]string, error) {
	pairs := [][2]string{
		{"aid", "8662"}, {"app_name", "novelread"}, {"version_code", "74332"}, {"version_name", "7.4.3.32"},
		{"channel", "vivo_8662_64"}, {"device_platform", "android"}, {"device_type", "PGT-AN10"},
		{"os_version", "12"}, {"update_version_code", "74332"}, {"iid", device.InstallID},
		{"cdid", device.ClientID}, {"device_id", device.DeviceID},
	}
	fullURL, headers, err := hongguoMobileV5SignedRequestFor(hongguoMobileV5API, pairs, body, now)
	if err == nil {
		headers["User-Agent"] = hongguoMobileV5UserAgent
		headers["Accept"] = "application/json"
		headers["Content-Encoding"] = "gzip"
	}
	return fullURL, headers, err
}

func hongguoMobileV5TabSignedRequest(device hongguoPlaybackDevice, videoID string, now time.Time) (string, map[string]string, error) {
	pairs := [][2]string{
		{"ac", "wifi"}, {"channel", "update_64"}, {"aid", "8662"}, {"app_name", "novelread"},
		{"version_code", "73932"}, {"version_name", "7.3.9.32"}, {"device_platform", "android"}, {"os", "android"},
		{"ssmix", "a"}, {"device_type", "25053RT47C"}, {"device_brand", "Redmi"}, {"language", "zh"},
		{"os_api", "36"}, {"os_version", "16"}, {"manifest_version_code", "73932"}, {"resolution", "1280*2772"},
		{"dpi", "520"}, {"update_version_code", "73932"}, {"host_abi", "arm64-v8a"}, {"dragon_device_type", "phone"},
		{"pv_player", "73932"}, {"compliance_status", "0"}, {"need_personal_recommend", "1"}, {"player_so_load", "1"},
		{"is_android_pad_screen", "0"}, {"iid", device.InstallID}, {"cdid", device.ClientID}, {"device_id", device.DeviceID},
		{"video_id", videoID}, {"video_id_type", "0"}, {"device_level", "1"}, {"need_all_video_definition", "1"},
		{"need_mp4_align", "0"}, {"use_os_player", "0"}, {"video_platform", "1024"}, {"source", "0"}, {"from_video_id", ""},
	}
	fullURL, headers, err := hongguoMobileV5SignedRequestFor(hongguoMobileV5TabAPI, pairs, nil, now)
	if err == nil {
		headers["User-Agent"] = hongguoMultiUserAgent
		headers["Accept-Language"] = "zh-CN,zh;q=0.9"
	}
	return fullURL, headers, err
}

func (downloader *Downloader) resolveHongguoMobileV5Media(ctx context.Context, videoID string) (providerMedia, error) {
	if !hongguoNumericID.MatchString(videoID) {
		return providerMedia{}, errors.New("红果视频 ID 无效")
	}
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	device, err := downloader.registerHongguoPlaybackDevice(ctx)
	if err != nil {
		return providerMedia{}, err
	}
	body, err := hongguoMobileV5Payload(videoID)
	if err != nil {
		return providerMedia{}, err
	}
	fullURL, headers, err := hongguoMobileV5SignedRequest(device, body, time.Now())
	if err != nil {
		return providerMedia{}, err
	}
	client := downloader.hongguoClient()
	if err = client.hongguoAppGate(ctx); err != nil {
		return providerMedia{}, err
	}
	defer client.hongguoAppGateRelease()
	ctx = hongguoPlaybackResponseContext(ctx, func(request *http.Request, response *http.Response) error {
		device.Cookies = hongguoMergePlaybackCookies(device.Cookies, request, response, time.Now())
		return nil
	})
	response, err := downloader.hongguoMultiFetchJSON(ctx, fullURL, http.MethodPost, headers, body, func(request *http.Request) {
		hongguoApplyPlaybackCookies(request, device, time.Now())
	})
	tabResponse := false
	if errors.Is(err, errHongguoEmptyPrimaryPlayback) {
		fullURL, headers, err = hongguoMobileV5TabSignedRequest(device, videoID, time.Now())
		if err == nil {
			response, err = downloader.hongguoMultiFetchJSON(ctx, fullURL, http.MethodGet, headers, nil, func(request *http.Request) {
				hongguoApplyPlaybackCookies(request, device, time.Now())
			})
		}
		if err != nil {
			return providerMedia{}, err
		}
		tabResponse = true
	} else if err != nil {
		return providerMedia{}, err
	}
	model, err := hongguoMobileV5Model(response, videoID, tabResponse)
	if err != nil {
		return providerMedia{}, err
	}
	return selectHongguoAppMedia(model)
}

func hongguoMobileV5Model(response map[string]any, videoID string, tabResponse bool) (map[string]any, error) {
	if code := firstNonEmpty(mapString(response, "Code", "code"), "0"); code != "0" {
		return nil, &hongguoPlaybackResponseError{fmt.Sprintf("红果新版 App 播放接口暂不可用（错误码 %s）", code)}
	}
	data, _ := response["data"].(map[string]any)
	groups := [2]string{"1004", "video_model_data"}
	if tabResponse {
		groups[0], groups[1] = groups[1], groups[0]
	}
	var entry map[string]any
	for _, group := range groups {
		entry, _ = nestedMap(data, group)[videoID].(map[string]any)
		if entry != nil {
			break
		}
	}
	if entry == nil {
		entry, _ = data[videoID].(map[string]any)
	}
	if entry == nil {
		return nil, errors.New("红果新版 App 未返回所选分集")
	}
	if id := mapString(entry, "item_id", "episode_id"); id != "" && id != videoID {
		return nil, errors.New("红果新版 App 返回了其他分集")
	}
	var model map[string]any
	switch raw := entry["video_model"].(type) {
	case map[string]any:
		model = raw
	case string:
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&model); err != nil {
			return nil, errors.New("红果新版 App 播放模型格式异常")
		}
	}
	if len(model) == 0 {
		return nil, errors.New("红果新版 App 未返回播放模型")
	}
	return model, nil
}
