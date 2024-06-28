package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/lmittmann/tint"
)

var (
	logNoTime bool
	logLevel  slog.Level
	confPath  string
)

func init() {
	flag.BoolVar(&logNoTime, "logNoTime", false, "Disable timestamps in log output")
	flag.TextVar(&logLevel, "logLevel", slog.LevelInfo, "Log level")
	flag.StringVar(&confPath, "confPath", "config.json", "Path to the configuration file")
}

func main() {
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var replaceAttr func(groups []string, attr slog.Attr) slog.Attr
	if logNoTime {
		replaceAttr = func(groups []string, attr slog.Attr) slog.Attr {
			if len(groups) == 0 && attr.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return attr
		}
	}

	logger := slog.New(tint.NewHandler(os.Stderr, &tint.Options{
		Level:       logLevel,
		ReplaceAttr: replaceAttr,
	}))

	_ = ctx
	_ = logger
}
