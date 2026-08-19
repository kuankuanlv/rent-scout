package log

import (
	"log/slog"

	"rent-scout/internal/pkglog"
)

func Collector(source string) *slog.Logger {
	return pkglog.Component(pkglog.SourceCollector(source))
}

func Info(source, msg string, args ...any) {
	Collector(source).Info(msg, args...)
}

func Warn(source, msg string, args ...any) {
	Collector(source).Warn(msg, args...)
}

func Error(source, msg string, args ...any) {
	Collector(source).Error(msg, args...)
}
