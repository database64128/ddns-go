package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	ddnsgo "github.com/database64128/ddns-go"
	"github.com/database64128/ddns-go/jsonhelper"
	"github.com/database64128/ddns-go/service"
	"github.com/database64128/ddns-go/tslog"
)

var (
	version    bool
	testConf   bool
	logNoColor bool
	logNoTime  bool
	logKVPairs bool
	logJSON    bool
	logLevel   slog.Level
	confPath   string
)

func init() {
	flag.BoolVar(&version, "version", false, "Print version and exit")
	flag.BoolVar(&testConf, "testConf", false, "Test the configuration file and exit")
	flag.BoolVar(&logNoColor, "logNoColor", false, "Disable colors in log output")
	flag.BoolVar(&logNoTime, "logNoTime", false, "Disable timestamps in log output")
	flag.BoolVar(&logKVPairs, "logKVPairs", false, "Use key=value pairs in log output")
	flag.BoolVar(&logJSON, "logJSON", false, "Use JSON in log output")
	flag.TextVar(&logLevel, "logLevel", slog.LevelInfo, "Log level")
	flag.StringVar(&confPath, "confPath", "config.json", "Path to the configuration file")
}

func main() {
	flag.Parse()

	if version {
		os.Stdout.WriteString("ddns-go\t" + ddnsgo.Version + "\n")
		if info, ok := debug.ReadBuildInfo(); ok {
			os.Stdout.WriteString(info.String())
		}
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-ctx.Done()
		stop()
	}()

	logCfg := tslog.Config{
		Level:          logLevel,
		NoColor:        logNoColor,
		NoTime:         logNoTime,
		UseTextHandler: logKVPairs,
		UseJSONHandler: logJSON,
	}
	logger := logCfg.NewLogger()
	logger.Info("ddns-go", slog.String("version", ddnsgo.Version))

	var cfg service.Config
	if err := jsonhelper.OpenAndDecodeDisallowUnknownFields(confPath, &cfg); err != nil {
		logger.Error("Failed to load configuration",
			slog.String("path", confPath),
			tslog.Err(err),
		)
		os.Exit(1)
	}

	svc, err := cfg.NewService(logger)
	if err != nil {
		logger.Error("Failed to create service", tslog.Err(err))
		os.Exit(1)
	}

	if testConf {
		logger.Info("Configuration file is valid")
		return
	}

	svc.Run(ctx)
}
