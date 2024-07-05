package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/database64128/ddns-go/internal/jsonhelper"
	"github.com/database64128/ddns-go/service"
	"github.com/lmittmann/tint"
)

var (
	logNoColor bool
	logNoTime  bool
	logLevel   slog.Level
	confPath   string
)

func init() {
	flag.BoolVar(&logNoColor, "logNoColor", false, "Disable colors in log output")
	flag.BoolVar(&logNoTime, "logNoTime", false, "Disable timestamps in log output")
	flag.TextVar(&logLevel, "logLevel", slog.LevelInfo, "Log level")
	flag.StringVar(&confPath, "confPath", "config.json", "Path to the configuration file")
}

func main() {
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-ctx.Done()
		stop()
	}()

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
		NoColor:     logNoColor,
	}))

	var cfg service.Config
	if err := jsonhelper.OpenAndDecodeDisallowUnknownFields(confPath, &cfg); err != nil {
		logger.LogAttrs(ctx, slog.LevelError, "Failed to load configuration",
			slog.String("path", confPath),
			slog.Any("error", err),
		)
		os.Exit(1)
	}

	cfg.Run(ctx, logger)
}
