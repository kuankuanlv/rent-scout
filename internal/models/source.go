package models

// Source 采集源标识；禁止业务里写裸 "douban"/"weibo"
type Source string

const (
	SourceDouban Source = "douban"
	SourceWeibo  Source = "weibo"
)

func (s Source) String() string { return string(s) }

func (s Source) Valid() bool {
	switch s {
	case SourceDouban, SourceWeibo:
		return true
	}
	return false
}

// ParseSource 非法值返回空 + false
func ParseSource(s string) (Source, bool) {
	src := Source(s)
	if src.Valid() {
		return src, true
	}
	return "", false
}

// KnownSources 配置多选与校验用
func KnownSources() []Source {
	return []Source{SourceDouban, SourceWeibo}
}
