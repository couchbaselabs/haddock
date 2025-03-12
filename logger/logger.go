// logger/logger.go
package logger

import (
	"flag"
	"os"
	"strings"
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	// Log is the global logger instance.
	Log  *zap.Logger
	once sync.Once
)

// Declare a command-line flag for the log level.
var zapLogLevelFlag = flag.String("zap-log-level", "info", "Set the zap log level (debug, info, warn, error, fatal, panic)")

// Init initializes the global logger using the log level from the command-line flag.
func Init() {
	once.Do(func() {
		// If flags haven't been parsed yet, parse them.
		if !flag.Parsed() {
			flag.Parse()
		}

		// Parse the flag value to get a zapcore.Level.
		logLevel := parseLogLevel(*zapLogLevelFlag)

		// Create encoder configuration.
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

		// Create a new zapcore.Core.
		core := zapcore.NewCore(
			zapcore.NewJSONEncoder(encoderConfig),
			zapcore.AddSync(os.Stdout),
			logLevel,
		)

		// Create the global logger.
		Log = zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))
	})
}

// parseLogLevel converts the flag string into a zapcore.Level.
func parseLogLevel(levelStr string) zapcore.Level {
	switch strings.ToLower(levelStr) {
	case "debug":
		return zapcore.DebugLevel
	case "info":
		return zapcore.InfoLevel
	case "warn", "warning":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	case "fatal":
		return zapcore.FatalLevel
	case "panic":
		return zapcore.PanicLevel
	default:
		return zapcore.InfoLevel
	}
}
