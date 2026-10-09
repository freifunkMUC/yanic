package log

import "log/slog"

const (
	AttrKeyError = "error"
)

func AttrError(err error) slog.Attr {
	return slog.Any(AttrKeyError, err)
}
