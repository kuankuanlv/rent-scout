package xiaohongshu

import (
	"sync"
)

type CRC32Helper struct {
	table [256]uint32
	once  sync.Once
}

var crc32Instance = &CRC32Helper{}

func (c *CRC32Helper) ensureTable() {
	c.once.Do(func() {
		poly := uint32(0xEDB88320)
		for d := 0; d < 256; d++ {
			r := uint32(d)
			for i := 0; i < 8; i++ {
				if r&1 != 0 {
					r = (r >> 1) ^ poly
				} else {
					r = r >> 1
				}
			}
			c.table[d] = r
		}
	})
}

// CRC32JSInt 复刻 Python 中的 (-1 ^ c ^ 0xEDB88320)>>>0逻辑
func CRC32JSInt(data []byte) uint32 {
	crc32Instance.ensureTable()
	c := uint32(0xFFFFFFFF)

	for _, b := range data {
		c = crc32Instance.table[(c^uint32(b))&0xFF] ^ (c >> 8)
	}

	return ^c
}
