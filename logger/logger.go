package logger

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/natefinch/lumberjack.v2"
)

type Config struct {
	Level      string `json:"level" yaml:"level"`
	Format     string `json:"format" yaml:"format"`
	LogFile    string `json:"logfile" yaml:"logfile"`
	ErrorFile  string `json:"errorfile" yaml:"errorfile"`
	MaxSize    int    `json:"max_size" yaml:"max_size"`
	MaxBackups int    `json:"max_backups" yaml:"max_backups"`
	MaxAge     int    `json:"max_age" yaml:"max_age"`
	Compress   bool   `json:"compress" yaml:"compress"`
	Console    *bool  `json:"console" yaml:"console"`
}

type SplitHandler struct {
	generalHandler slog.Handler
	errorHandler   slog.Handler
}

func (h *SplitHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.generalHandler.Enabled(ctx, level) || h.errorHandler.Enabled(ctx, level)
}

func (h *SplitHandler) Handle(ctx context.Context, r slog.Record) error {
	var err1, err2 error
	if h.generalHandler.Enabled(ctx, r.Level) {
		err1 = h.generalHandler.Handle(ctx, r)
	}
	if r.Level >= slog.LevelError {
		err2 = h.errorHandler.Handle(ctx, r)
	}
	if err1 != nil {
		return err1
	}
	return err2
}

func (h *SplitHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &SplitHandler{
		generalHandler: h.generalHandler.WithAttrs(attrs),
		errorHandler:   h.errorHandler.WithAttrs(attrs),
	}
}

func (h *SplitHandler) WithGroup(name string) slog.Handler {
	return &SplitHandler{
		generalHandler: h.generalHandler.WithGroup(name),
		errorHandler:   h.errorHandler.WithGroup(name),
	}
}

func InitLogger(cfg Config) {
	// 1. Resolve level
	var level slog.Level
	switch strings.ToLower(cfg.Level) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	optsGeneral := &slog.HandlerOptions{Level: level}
	optsError := &slog.HandlerOptions{Level: slog.LevelError}

	serverMode := isServer()
	consoleEnabled := serverMode
	if cfg.Console != nil {
		consoleEnabled = *cfg.Console
	}

	var writerGeneral io.Writer
	var writerError io.Writer

	// Log files are only enabled on server
	logFile := ""
	errorFile := ""
	if serverMode {
		logFile = cfg.LogFile
		errorFile = cfg.ErrorFile
	}

	// Setup general writer
	if logFile != "" {
		maxSize := cfg.MaxSize
		if maxSize == 0 {
			maxSize = 10
		}
		maxBackups := cfg.MaxBackups
		if maxBackups == 0 {
			maxBackups = 5
		}
		maxAge := cfg.MaxAge
		if maxAge == 0 {
			maxAge = 28
		}

		lumberjackGeneral := &lumberjack.Logger{
			Filename:   logFile,
			MaxSize:    maxSize,
			MaxBackups: maxBackups,
			MaxAge:     maxAge,
			Compress:   cfg.Compress,
		}
		if consoleEnabled {
			writerGeneral = io.MultiWriter(os.Stdout, lumberjackGeneral)
		} else {
			writerGeneral = lumberjackGeneral
		}
	} else {
		if consoleEnabled {
			writerGeneral = os.Stdout
		} else {
			writerGeneral = io.Discard
		}
	}

	// Setup error writer
	if serverMode {
		if errorFile == "" && logFile != "" {
			// Default to "errors" directory under parent
			baseName := filepath.Base(logFile)
			errorFile = filepath.Join("errors", baseName)
		}

		if errorFile != "" {
			maxSize := cfg.MaxSize
			if maxSize == 0 {
				maxSize = 10
			}
			maxBackups := cfg.MaxBackups
			if maxBackups == 0 {
				maxBackups = 5
			}
			maxAge := cfg.MaxAge
			if maxAge == 0 {
				maxAge = 28
			}

			lumberjackError := &lumberjack.Logger{
				Filename:   errorFile,
				MaxSize:    maxSize,
				MaxBackups: maxBackups,
				MaxAge:     maxAge,
				Compress:   cfg.Compress,
			}
			writerError = lumberjackError
		} else {
			writerError = io.Discard
		}
	} else {
		writerError = io.Discard
	}

	var handlerGeneral slog.Handler
	var handlerError slog.Handler

	if strings.ToLower(cfg.Format) == "json" {
		handlerGeneral = slog.NewJSONHandler(writerGeneral, optsGeneral)
		handlerError = slog.NewJSONHandler(writerError, optsError)
	} else {
		handlerGeneral = slog.NewTextHandler(writerGeneral, optsGeneral)
		handlerError = slog.NewTextHandler(writerError, optsError)
	}

	// Wrap in SplitHandler
	compositeHandler := &SplitHandler{
		generalHandler: handlerGeneral,
		errorHandler:   handlerError,
	}

	slog.SetDefault(slog.New(compositeHandler))
}

func isServer() bool {
	env := strings.ToLower(os.Getenv("ENV"))
	appEnv := strings.ToLower(os.Getenv("APP_ENV"))
	if env == "production" || env == "prod" || env == "server" ||
		appEnv == "production" || appEnv == "prod" || appEnv == "server" {
		return true
	}
	if runtime.GOOS == "linux" {
		return true
	}
	return false
}

