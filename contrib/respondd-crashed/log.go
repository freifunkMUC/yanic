package main

import (
	"log/slog"
	"os"

	"github.com/ThreeDotsLabs/humanslog"
)

var (
	logFormat  string
	logSource  bool
	timestamps bool
	logLevel   slog.Level
)

const (
	logFormatJSON  = "json"
	logFormatText  = "text"
	logFormatHuman = "human"
)

func setupLog() {
	handlerOption := &slog.HandlerOptions{
		Level:     logLevel,
		AddSource: logSource,
	}
	if !timestamps {
		handlerOption.ReplaceAttr = func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		}
	}
	var h slog.Handler
	switch logFormat {
	case logFormatHuman:
		h = humanslog.NewHandler(os.Stdout, &humanslog.Options{
			HandlerOptions: handlerOption,
		})
	case logFormatText:
		h = slog.NewTextHandler(os.Stdout, handlerOption)
	default:
		h = slog.NewJSONHandler(os.Stdout, handlerOption)
	}
	slog.SetDefault(slog.New(h))
}
