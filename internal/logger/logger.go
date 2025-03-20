package logger

import (
	"os"
	"strings"
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	// Log is the global logger instance
	Log  *zap.Logger
	once sync.Once
)

// Init initializes the global logger
func Init() {
	once.Do(func() {
		// Read log level from environment (set in operator.yaml)
		logLevel := getLogLevelFromEnv()

		// Create encoder config
		encoderConfig := zapcore.EncoderConfig{
			TimeKey:        "ts",
			LevelKey:       "level",
			NameKey:        "logger",
			CallerKey:      "caller",
			FunctionKey:    zapcore.OmitKey,
			MessageKey:     "msg",
			StacktraceKey:  "stacktrace",
			LineEnding:     zapcore.DefaultLineEnding,
			EncodeLevel:    zapcore.CapitalLevelEncoder,
			EncodeTime:     zapcore.ISO8601TimeEncoder,
			EncodeDuration: zapcore.SecondsDurationEncoder,
			EncodeCaller:   zapcore.ShortCallerEncoder,
		}

		// Create core
		core := zapcore.NewCore(
			zapcore.NewJSONEncoder(encoderConfig),
			zapcore.AddSync(os.Stdout),
			logLevel,
		)

		// Create logger
		Log = zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))
	})
}

// getLogLevelFromEnv reads ZAP_LOG_LEVEL from environment and returns corresponding zapcore.Level
func getLogLevelFromEnv() zapcore.Level {
	// Default to info level
	level := zapcore.InfoLevel

	// Try to get from environment
	levelStr := strings.ToLower(os.Getenv("ZAP_LOG_LEVEL"))
	if levelStr == "" {
		return level
	}

	// Map string level to zapcore.Level
	switch levelStr {
	case "debug":
		level = zapcore.DebugLevel
	case "info":
		level = zapcore.InfoLevel
	case "warn", "warning":
		level = zapcore.WarnLevel
	case "error":
		level = zapcore.ErrorLevel
	case "fatal":
		level = zapcore.FatalLevel
	case "panic":
		level = zapcore.PanicLevel
	}

	return level
}
