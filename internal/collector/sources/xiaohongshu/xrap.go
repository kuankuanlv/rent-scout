package xiaohongshu

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"math/rand"
	"time"
)

type XRapOptions struct {
	Timestamp int64
}

func _fieldU64(tag uint16, val uint64) []byte {
	buf := make([]byte, 10)
	binary.BigEndian.PutUint16(buf[0:2], tag)
	binary.BigEndian.PutUint64(buf[2:10], val)
	return buf
}

func _fieldU32(tag uint16, val uint32) []byte {
	buf := make([]byte, 6)
	binary.BigEndian.PutUint16(buf[0:2], tag)
	binary.BigEndian.PutUint32(buf[2:6], val)
	return buf
}

func _fieldBlob(tag uint16, data []byte) []byte {
	buf := make([]byte, 6+len(data))
	binary.BigEndian.PutUint16(buf[0:2], tag)
	binary.BigEndian.PutUint32(buf[2:6], uint32(len(data)))
	copy(buf[6:], data)
	return buf
}

// XRapParam 实现
func XRapParam(api string, data []byte, opts XRapOptions) (string, error) {
	ts := opts.Timestamp
	if ts == 0 {
		ts = time.Now().UnixMilli()
	}
	
	nonce := uint32(rand.Int31())
	key := make([]byte, 16)
	rand.Read(key)
	
	buf := new(bytes.Buffer)
	buf.Write(_fieldU64(0x03E8, uint64(ts)))
	buf.Write(_fieldU32(0x03E9, nonce))
	buf.Write(_fieldBlob(0x03EA, key))
	
	// CRC32 计算
	hashInput := []byte(api)
	hashInput = append(hashInput, data...)
	crc := CRC32JSInt(hashInput)
	buf.Write(_fieldU32(0x03EB, uint32(crc)))
	
	// 这里的 gzip patch 逻辑需要注意，python 实现中 mtime 需要置 0
	// gzip.Writer 默认使用当前时间，需要调整
	var b bytes.Buffer
	gw, _ := gzip.NewWriterLevel(&b, gzip.BestCompression)
	// 手动处理 gzip header 中的 mtime 
	// 在 Go 中比较难直接操作 gzip.Writer 内部 buffer，
	// 考虑先生成后 patch
	gw.Write(buf.Bytes())
	gw.Close()

	// Patch gzip mtime (bytes 4-7) to 0
	// Gzip 格式：
	// Bytes 0-1: ID1, ID2 (0x1f, 0x8b)
	// Byte 2: CM (8 = deflate)
	// Byte 3: FLG
	// Bytes 4-7: MTIME
	res := b.Bytes()
	if len(res) >= 10 {
		res[4] = 0
		res[5] = 0
		res[6] = 0
		res[7] = 0
	}

	encoded := base64.StdEncoding.EncodeToString(res)
	return fmt.Sprintf("1_%s", encoded), nil
}
