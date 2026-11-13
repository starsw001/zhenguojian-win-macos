package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"duanjuapp/native/licensing"
	"duanjuapp/native/portable"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/crypto/pbkdf2"
)

type customer struct {
	AccountMode    string `json:"accountMode,omitempty"`
	DistributionID string `json:"distributionId"`
	Username       string `json:"username"`
	Password       string `json:"password"`
	Salt           string `json:"salt"`
	PasswordHash   string `json:"passwordHash"`
	Iterations     int    `json:"iterations"`
	Months         int    `json:"months"`
}

// randomInt 生成小于给定上限的随机整数。
func randomInt(max int) int {
	value, err := rand.Int(rand.Reader, big.NewInt(int64(max)))
	if err != nil {
		panic(err)
	}
	return int(value.Int64())
}

// randomText 从指定字符集中生成固定长度的随机文本。
func randomText(length int, alphabet string) string {
	text := make([]byte, length)
	for i := range text {
		text[i] = alphabet[randomInt(len(alphabet))]
	}
	return string(text)
}

// newUsername 生成满足软件账号格式要求的随机邮箱式用户名。
func newUsername() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	return randomText(6+randomInt(7), alphabet) + "@" + randomText(2+randomInt(11), alphabet) + ".com"
}

// newCustomer 生成给定授权月数对应的离线客户资料。
func newCustomer(months int) (customer, error) {
	if months < 1 || months > 12 {
		return customer{}, errors.New("有效期必须为 1–12 个月")
	}
	alphabet := "abcdefghijklmnopqrstuvwxyz0123456789"
	letters := []byte{byte('A' + randomInt(26)), byte('a' + randomInt(26)), "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"[randomInt(52)]}
	password := append(letters, byte('0'+randomInt(10)), byte('0'+randomInt(10)), byte('0'+randomInt(10)), '@', '#')
	for i := len(password) - 1; i > 0; i-- {
		j := randomInt(i + 1)
		password[i], password[j] = password[j], password[i]
	}
	salt := make([]byte, 24)
	if _, err := rand.Read(salt); err != nil {
		return customer{}, err
	}
	value := customer{DistributionID: randomText(32, alphabet), Username: newUsername(), Password: string(password), Salt: base64.RawURLEncoding.EncodeToString(salt), Iterations: 310000, Months: months}
	value.PasswordHash = base64.RawURLEncoding.EncodeToString(pbkdf2.Key(password, salt, value.Iterations, 32, sha256.New))
	return value, nil
}

// writePrivate 以受限权限写入私有分发资料。
func writePrivate(path string, value []byte) error {
	if path == "" {
		return errors.New("必须指定输出路径")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := licensing.RestrictPrivateFile(path); err != nil {
		return err
	}
	if _, err := file.Write(value); err != nil {
		return err
	}
	return file.Sync()
}

// readSigningKey 读取并解析分发签名私钥。
func readSigningKey(path string) (ed25519.PrivateKey, error) {
	text, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	key, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(string(text)))
	if err != nil || len(key) != ed25519.PrivateKeySize {
		return nil, errors.New("分发签名私钥格式无效")
	}
	return ed25519.PrivateKey(key), nil
}

// signBundle 校验客户资料与发布身份，并为软件目录生成签名清单。
func signBundle(bundle, customerPath, keyPath, edition, version, expectedPublic string) error {
	if edition != "hongguoshang" && edition != "zhenguoshang" {
		return errors.New("软件版本类型无效")
	}
	if !regexp.MustCompile(`^\d+\.\d+\.\d+\+\d+$`).MatchString(version) {
		return errors.New("软件版本号无效")
	}
	var user customer
	body, err := os.ReadFile(customerPath)
	if err != nil || json.Unmarshal(body, &user) != nil {
		return errors.New("无法读取管理员专属登录资料")
	}
	if user.AccountMode != "" && user.AccountMode != "universal" || user.Months < 1 || user.Months > 12 || user.DistributionID == "" || user.AccountMode == "" && (user.Username == "" || user.Iterations < 200000 || user.PasswordHash == "") || user.AccountMode == "universal" && (user.Username != "" || user.PasswordHash != "" || user.Salt != "" || user.Iterations != 0 || user.Password != "" || user.Months != 12) {
		return errors.New("专属登录资料无效")
	}
	key, err := readSigningKey(keyPath)
	if err != nil {
		return err
	}
	if expectedPublic == "" || base64.RawURLEncoding.EncodeToString(key.Public().(ed25519.PublicKey)) != expectedPublic {
		return errors.New("分发签名私钥与编译进客户端的公钥不匹配")
	}
	root, err := filepath.Abs(bundle)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(filepath.Join(root, "distribution.json")); !os.IsNotExist(err) {
		return errors.New("分发签名文件已存在，不覆盖已分发安装包")
	}
	files := map[string]string{}
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("运行目录不能包含链接")
		}
		if entry.IsDir() {
			if path != root && (portable.MutableDirectory(entry.Name()) || strings.HasPrefix(entry.Name(), ".")) {
				return errors.New("运行目录含用户或验证数据，拒绝封装")
			}
			return nil
		}
		name, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if strings.HasSuffix(strings.ToLower(name), ".mp4") || strings.HasSuffix(strings.ToLower(name), ".key") || strings.HasSuffix(strings.ToLower(name), ".log") {
			return errors.New("运行目录含视频、密钥或日志，拒绝封装")
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		digest := sha256.New()
		if _, err := io.Copy(digest, file); err != nil {
			return err
		}
		files[filepath.ToSlash(name)] = hex.EncodeToString(digest.Sum(nil))
		return nil
	})
	if err != nil {
		return err
	}
	for _, name := range []string{edition + ".exe", "duanju_core.dll", "data/app.so"} {
		if files[name] == "" {
			return fmt.Errorf("运行目录缺少 %s", name)
		}
	}
	distribution := licensing.Distribution{Category: licensing.DownloadLicenseCategory, AccountMode: user.AccountMode, Protocol: licensing.Protocol, DistributionID: user.DistributionID, ReleaseID: randomText(32, "abcdefghijklmnopqrstuvwxyz0123456789"), Edition: edition, Version: version, Username: user.Username, Salt: user.Salt, PasswordHash: user.PasswordHash, Iterations: user.Iterations, Months: user.Months, Files: files}
	permission, err := json.Marshal(licensing.ActivationPermit{Category: licensing.DownloadLicenseCategory, AccountMode: user.AccountMode, Protocol: licensing.Protocol, Kind: "activation-permit", DistributionID: user.DistributionID, Edition: edition, Months: user.Months, Username: user.Username})
	if err != nil {
		return err
	}
	distribution.Permit = licensing.Envelope{Payload: base64.RawURLEncoding.EncodeToString(permission), Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, permission))}
	payload, err := json.Marshal(distribution)
	if err != nil {
		return err
	}
	envelope := licensing.Envelope{Payload: base64.RawURLEncoding.EncodeToString(payload), Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, payload))}
	output, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return err
	}
	return writePrivate(filepath.Join(root, "distribution.json"), output)
}

// run 解析管理命令参数，执行客户资料生成或分发签名操作。
func run(args []string) error {
	if len(args) == 0 {
		return errors.New("用法：license-admin init-key|customer|seal")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	out := flags.String("out", "", "输出到管理员受保护路径，禁止放入源码或分发包")
	months := flags.Int("months", 1, "1–12个月，服务端key到期时间仍为上限")
	bundle := flags.String("bundle", "", "干净Windows运行目录")
	user := flags.String("customer", "", "管理员专属资料文件")
	key := flags.String("signing-key", "", "管理员分发签名私钥文件")
	edition := flags.String("edition", "", "hongguoshang或zhenguoshang")
	version := flags.String("version", "", "客户端版本与构建号")
	public := flags.String("expected-public-key", "", "与原生核心构建一致的分发公钥")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	switch args[0] {
	case "save-private":
		data, err := io.ReadAll(io.LimitReader(os.Stdin, (1<<20)+1))
		if err != nil || len(data) == 0 || len(data) > 1<<20 {
			return errors.New("私有输入为空或超出上限")
		}
		if !filepath.IsAbs(*out) {
			return errors.New("私有输出必须为管理员指定的绝对路径")
		}
		if err := writePrivate(*out, data); err != nil {
			return err
		}
		fmt.Println("私有资料已受保护保存，内容未输出")
	case "init-key":
		public, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		if err := writePrivate(*out, []byte(base64.RawURLEncoding.EncodeToString(private)+"\n")); err != nil {
			return err
		}
		fmt.Println("PublicKey=" + base64.RawURLEncoding.EncodeToString(public))
	case "customer":
		if !filepath.IsAbs(*out) || !strings.HasSuffix(strings.ToLower(*out), ".credentials.json") {
			return errors.New("专属资料必须保存到源码目录外的绝对路径，文件后缀为 .credentials.json")
		}
		value, err := newCustomer(*months)
		if err != nil {
			return err
		}
		body, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return err
		}
		if err := writePrivate(*out, body); err != nil {
			return err
		}
		fmt.Println("专属登录资料已保存；不在终端打印密码：" + *out)
	case "seal":
		if err := signBundle(*bundle, *user, *key, *edition, *version, *public); err != nil {
			return err
		}
		fmt.Println("已签名专属分发清单；未写入明文密码或私钥")
	default:
		return errors.New("管理员操作无效")
	}
	return nil
}

// main 启动离线分发管理工具，并将执行错误返回到进程状态。
func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
