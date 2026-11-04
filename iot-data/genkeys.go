// genkeys 生成一对 ECDSA(P-256) 测试密钥，并对一条上报数据做签名，
// 输出 shell 可 source 的 DEV_PUBKEY / DEV_SIG 变量，供 acceptance-test.sh 使用。
//
// 用法：go run genkeys.go <deviceID> <dataType> <value> <rawTimestamp>
// 默认：go run genkeys.go devA temperature 25.5 0
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func main() {
	deviceID, dataType, value := "devA", "temperature", "25.5"
	var rawTimestamp int64 = 0
	if len(os.Args) >= 5 {
		deviceID = os.Args[1]
		dataType = os.Args[2]
		value = os.Args[3]
		rawTimestamp, _ = strconv.ParseInt(os.Args[4], 10, 64)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		panic(err)
	}

	// 与链码 verifyReportSignature 保持一致的签名消息：deviceID|dataType|value|rawTimestamp
	msg := strings.Join([]string{deviceID, dataType, value, strconv.FormatInt(rawTimestamp, 10)}, "|")
	digest := sha256.Sum256([]byte(msg))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		panic(err)
	}
	// 把 r、s 各补成 32 字节，拼接成 64 字节的 r||s
	rb, sb := r.Bytes(), s.Bytes()
	rp := make([]byte, 32)
	sp := make([]byte, 32)
	copy(rp[32-len(rb):], rb)
	copy(sp[32-len(sb):], sb)
	sig := append(rp, sp...)

	fmt.Printf("export DEV_PUBKEY='%s'\n", hex.EncodeToString(pubDER))
	fmt.Printf("export DEV_SIG='%s'\n", hex.EncodeToString(sig))
}
