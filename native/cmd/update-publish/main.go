package main

import (
	"duanjuapp/native/secureupdate"
	"flag"
	"fmt"
	"os"
)

// main 解析更新发布参数并生成 ZIP 与签名清单。
func main() {
	bundle := flag.String("bundle", "", "")
	out := flag.String("out", "", "")
	key := flag.String("key-file", "", "")
	pub := flag.String("public-key", "", "")
	ed := flag.String("edition", "", "")
	id := flag.String("distribution-id", "", "")
	v := flag.String("version", "", "")
	flag.Parse()
	r, e := secureupdate.Publish(*bundle, *out, *key, *pub, *ed, *id, *v)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	fmt.Printf("%s size=%d SHA256=%s\n", r.Archive, r.Size, r.SHA256)
}
