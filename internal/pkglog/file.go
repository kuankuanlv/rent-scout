package pkglog

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	logFilePrefix  = "rent-scout-"
	defaultLogsRel = "logs"
)

type dailyFile struct {
	dir string
	mu  sync.Mutex
	day string
	f   *os.File
}

func newDailyFile(dir string) *dailyFile {
	return &dailyFile{dir: dir}
}

func (d *dailyFile) Write(p []byte) (int, error) {
	day := time.Now().Format("2006-01-02")
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.f == nil || d.day != day {
		if err := d.reopenLocked(day); err != nil {
			return 0, err
		}
	}
	return d.f.Write(p)
}

func (d *dailyFile) reopenLocked(day string) error {
	if d.f != nil {
		_ = d.f.Close()
		d.f = nil
	}
	if err := os.MkdirAll(d.dir, 0o755); err != nil {
		return err
	}
	name := filepath.Join(d.dir, logFilePrefix+day+".log")
	f, err := os.OpenFile(name, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	d.f = f
	d.day = day
	return nil
}

// LogDir 和数据库一样：相对当前工作目录。默认 logs，可用环境变量 LOG_DIR 覆盖。
func LogDir() string {
	return defaultLogDir()
}

func defaultLogDir() string {
	if d := os.Getenv("LOG_DIR"); d != "" {
		return d
	}
	return defaultLogsRel
}

func ensureLogDir(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建日志目录: %w", err)
	}
	return dir, nil
}
