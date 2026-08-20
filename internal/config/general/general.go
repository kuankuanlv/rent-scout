package general

type Config struct {
	Server ServerConfig
	Log    LogConfig
}

// ServerConfig HTTP 监听
type ServerConfig struct {
	Addr       string // 监听，如 :7777
	PublicBase string // 对外根地址，如 http://192.168.1.8:7777；空则发通知时自动用局域网 IP
}

// 控制台内存日志条数：默认 1000；探测 raw 单条可到数 KB，调太大占内存
const (
	DefaultLogMemoryLines = 1000
	MinLogMemoryLines     = 100
	MaxLogMemoryLines     = 10000
)

// LogConfig 日志（path 空 = stdout，配了才写文件轮转）
type LogConfig struct {
	Level       string
	Format      string
	Path        string // 可选：日志文件路径（空=stdout）
	MemoryLines int    // 管理台 ring 保留条数，占进程内存
}

func ApplyDefaults(s *ServerConfig, l *LogConfig) {
	if s.Addr == "" {
		s.Addr = ":7777"
	}
	if l.Level == "" {
		l.Level = "info"
	}
	if l.Format == "" {
		l.Format = "text"
	}
	if l.MemoryLines <= 0 {
		l.MemoryLines = DefaultLogMemoryLines
	}
}
