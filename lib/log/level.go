package log

import (
	"context"
	"log/slog"
)

const LevelPanic slog.Level = 12

func Panic(msg string, args ...any) {
	PanicLogger(slog.Default(), msg, args...)
}

func PanicLogger(logger *slog.Logger, msg string, args ...any) {
	logger.Log(context.Background(), LevelPanic, msg, args...)
	panic(msg)
}
