package logger

import (
	"log"
	"os"
	"strings"
)

// Logger wraps standard log package with connection error filtering
type Logger struct {
	logConnErrors bool
}

// New creates a new logger
func New(logConnErrors bool) *Logger {
	return &Logger{
		logConnErrors: logConnErrors,
	}
}

// NewFromEnv creates a logger from LOG_CONNECTION_ERRORS env var
func NewFromEnv() *Logger {
	envVal := strings.ToLower(strings.TrimSpace(os.Getenv("LOG_CONNECTION_ERRORS")))
	logConnErrors := envVal == "true" || envVal == "1" || envVal == "yes"
	return New(logConnErrors)
}

// Printf logs a formatted message (same as log.Printf)
func (l *Logger) Printf(format string, args ...interface{}) {
	log.Printf(format, args...)
}

// Fatal logs and exits (same as log.Fatal)
func (l *Logger) Fatal(v ...interface{}) {
	log.Fatal(v...)
}

// Fatalf logs formatted message and exits (same as log.Fatalf)
func (l *Logger) Fatalf(format string, args ...interface{}) {
	log.Fatalf(format, args...)
}

// LogConnectionError logs a connection error only if enabled
// This matches the existing LOG_CONNECTION_ERRORS behavior
func (l *Logger) LogConnectionError(format string, args ...interface{}) {
	if l.logConnErrors {
		log.Printf(format, args...)
	}
}

// Package-level default logger
var defaultLogger = NewFromEnv()

// Printf logs using default logger
func Printf(format string, args ...interface{}) {
	defaultLogger.Printf(format, args...)
}

// Fatal logs and exits using default logger
func Fatal(v ...interface{}) {
	defaultLogger.Fatal(v...)
}

// Fatalf logs and exits using default logger  
func Fatalf(format string, args ...interface{}) {
	defaultLogger.Fatalf(format, args...)
}

// LogConnectionError logs connection error using default logger
func LogConnectionError(format string, args ...interface{}) {
	defaultLogger.LogConnectionError(format, args...)
}