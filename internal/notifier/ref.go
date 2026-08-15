package notifier

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
)

const postRefPepper = "rent-scout-post-ref"

func refKey(secret string) []byte {
	s := secret
	if s == "" {
		s = postRefPepper
	}
	sum := sha256.Sum256([]byte("rs-pref|" + s))
	return sum[:]
}

// SealPostRef 把帖子 id 封成链接里的随机串，不出现数字 id
func SealPostRef(id int64, secret string) string {
	key := refKey(secret)
	block, err := aes.NewCipher(key[:16])
	if err != nil {
		return ""
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return ""
	}
	var plain [8]byte
	binary.BigEndian.PutUint64(plain[:], uint64(id))
	ns := sha256.Sum256(append(append([]byte{}, key...), plain[:]...))
	nonce := ns[:gcm.NonceSize()]
	ct := gcm.Seal(nil, nonce, plain[:], nil)
	out := append(append([]byte{}, nonce...), ct...)
	return base64.RawURLEncoding.EncodeToString(out)
}

// OpenPostRef 解开链接里的随机串；对不上就当无效
func OpenPostRef(token, secret string) (int64, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return 0, fmt.Errorf("引用无效")
	}
	key := refKey(secret)
	block, err := aes.NewCipher(key[:16])
	if err != nil {
		return 0, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return 0, err
	}
	ns := gcm.NonceSize()
	if len(raw) < ns+gcm.Overhead() {
		return 0, fmt.Errorf("引用无效")
	}
	plain, err := gcm.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil || len(plain) != 8 {
		return 0, fmt.Errorf("引用无效")
	}
	return int64(binary.BigEndian.Uint64(plain)), nil
}
