package logharbour

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/go-playground/validator/v10"
)

const DefaultPriority = Info

// Logger provides a structured interface for logging.
// It's designed for each goroutine to have its own instance.
// Logger is safe for concurrent use. However, it's not recommended
// to share a Logger instance across multiple goroutines.
//
// If the writer is a FallbackWriter and validation of a log entry fails,
// the Logger will automatically write the invalid entry to the FallbackWriter's fallback writer.
// If writing to fallback writer also fails then it writes to STDERR.
//
// The 'With' prefixed methods in the Logger are used to create a new Logger instance
// with a specific field set to a new value. These methods  create a copy of the current Logger,
// then set the desired field to the new value, and finally return the new Logger.
// This approach provides a flexible way to create a new Logger with specific settings,
// without having to provide all settings at once or change the settings of an existing Logger.
type Logger struct {
	appName        string              // Name of the application.
	system         string              // System where the application is running.
	module         string              // Module or subsystem within the application.
	priority       LogPriority         // Priority level of the log messages.
	who            string              // User or service performing the operation.
	op             string              // Operation being performed.
	whatClass      string              // Class of the object instance involved.
	whatInstanceId string              // Unique ID of the object instance.
	status         Status              // Status of the operation.
	remoteIP       string              // IP address of the remote endpoint.
	writer         io.Writer           // Writer interface for log entries.
	validator      *validator.Validate // Validator for log entries.
	mu             sync.Mutex          // Mutex for thread-safe operations.
}

// clone creates and returns a new Logger with the same values as the original.
func (l *Logger) clone() *Logger {
	return &Logger{
		appName:        l.appName,
		system:         l.system,
		module:         l.module,
		priority:       l.priority,
		who:            l.who,
		op:             l.op,
		whatClass:      l.whatClass,
		whatInstanceId: l.whatInstanceId,
		status:         l.status,
		remoteIP:       l.remoteIP,
		writer:         l.writer,
		validator:      l.validator,
	}
}

// NewLogger creates a new Logger with the specified application name and writer.
// We recommend using NewLoggerWithFallback instead of this method.
func NewLogger(appName string, writer io.Writer) *Logger {
	return &Logger{
		appName:   appName,
		system:    getSystemName(),
		writer:    writer,
		validator: validator.New(),
		priority:  DefaultPriority,
	}
}

// NewLoggerWithFallback creates a new Logger with a fallback writer.
// The fallback writer is used if the primary writer fails or if validation of a log entry fails.
func NewLoggerWithFallback(appName string, fallbackWriter *FallbackWriter) *Logger {
	return &Logger{
		appName:   appName,
		system:    getSystemName(),
		writer:    fallbackWriter,
		validator: validator.New(),
		priority:  DefaultPriority,
	}
}

// WithWho returns a new Logger with the 'who' field set to the specified value.
func (l *Logger) WithWho(who string) *Logger {
	newLogger := l.clone() // Create a copy of the logger
	newLogger.who = who    // Change the 'who' field
	return newLogger       // Return the new logger
}

// WithModule returns a new Logger with the 'module' field set to the specified value.
func (l *Logger) WithModule(module string) *Logger {
	newLogger := l.clone()
	newLogger.module = module
	return newLogger
}

// WithOp returns a new Logger with the 'op' field set to the specified value.
func (l *Logger) WithOp(op string) *Logger {
	newLogger := l.clone()
	newLogger.op = op
	return newLogger
}

// WithWhatClass returns a new Logger with the 'whatClass' field set to the specified value.
func (l *Logger) WithWhatClass(whatClass string) *Logger {
	newLogger := l.clone()
	newLogger.whatClass = whatClass
	return newLogger
}

// WithWhatInstanceId returns a new Logger with the 'whatInstanceId' field set to the specified value.
func (l *Logger) WithWhatInstanceId(whatInstanceId string) *Logger {
	newLogger := l.clone()
	newLogger.whatInstanceId = whatInstanceId
	return newLogger
}

// WithStatus returns a new Logger with the 'status' field set to the specified value.
func (l *Logger) WithStatus(status Status) *Logger {
	newLogger := l.clone()
	newLogger.status = status
	return newLogger
}

// WithPriority returns a new Logger with the 'priority' field set to the specified value.
//
// There are shortcut functions like Info(), Warn(), etc. provided as a convenient way to
// set the priority level for a single call.
// Each of these function creates a new Logger instance with the specified priority and returns it.
// The original Logger instance remains unchanged.
// For example, instead of writing
//
//	logger.WithPriority(logharbour.Info).LogChange(...),
//
// you can simply write
//
//	logger.Info().LogChange(...)
func (l *Logger) WithPriority(priority LogPriority) *Logger {
	newLogger := l.clone()
	newLogger.priority = priority
	return newLogger
}

// WithRemoteIP returns a new Logger with the 'remoteIP' field set to the specified value.
func (l *Logger) WithRemoteIP(remoteIP string) *Logger {
	newLogger := l.clone()
	newLogger.remoteIP = remoteIP
	return newLogger
}

// log writes a log entry. It locks the Logger's mutex to prevent concurrent write operations.
// If there's a problem with writing the log entry or if the log entry is invalid,
// it attempts to write the error and the log entry to the fallback writer (if available).
// If writing to the fallback writer fails or if the fallback writer is not available,
// it writes the error and the log entry to stderr.
func (l *Logger) log(entry LogEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()

	entry.AppName = l.appName
	if !l.shouldLog(entry.Priority) {
		return
	}
	if err := l.validator.Struct(entry); err != nil {
		// Check if the writer is a FallbackWriter
		if fw, ok := l.writer.(*FallbackWriter); ok {
			// Write to the fallback writer if validation fails
			if err := formatAndWriteEntry(fw.fallback, entry); err != nil {
				// If writing to the fallback writer fails, write to stderr
				fmt.Fprintf(os.Stderr, "Error: %v, LogEntry: %+v\n", err, entry)
			}
		} else {
			fmt.Fprintf(os.Stderr, "Error: %v, LogEntry: %+v\n", err, entry)
		}
		return
	}
	if err := formatAndWriteEntry(l.writer, entry); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v, LogEntry: %+v\n", err, entry)
	}
}

// shouldLog determines whether a log entry should be written based on its priority.
func (l *Logger) shouldLog(p LogPriority) bool {
	return p >= l.priority
}

// formatAndWriteEntry formats a log entry as JSON and writes it to the Logger's writer.
func formatAndWriteEntry(writer io.Writer, entry LogEntry) error {
	formattedEntry, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	formattedEntry = append(formattedEntry, '\n')
	_, writeErr := writer.Write(formattedEntry)
	return writeErr
}

// newLogEntry creates a new log entry with the specified message and data.
func (l *Logger) newLogEntry(message string, data any) LogEntry {
	return LogEntry{
		AppName:        l.appName,
		System:         l.system,
		Module:         l.module,
		Priority:       l.priority,
		Who:            l.who,
		Op:             l.op,
		When:           time.Now().UTC(),
		WhatClass:      l.whatClass,
		WhatInstanceId: l.whatInstanceId,
		Status:         l.status,
		RemoteIP:       l.remoteIP,
		Message:        message,
		Data:           data,
	}
}

// LogDataChange logs a data change event.
func (l *Logger) LogDataChange(message string, data ChangeInfo) {
	entry := l.newLogEntry(message, data)
	entry.Type = Change
	l.log(entry)
}

// LogActivity logs an activity event.
func (l *Logger) LogActivity(message string, data ActivityInfo) {
	entry := l.newLogEntry(message, data)
	entry.Type = Activity
	l.log(entry)
}

// LogDebug logs a debug event.
func (l *Logger) LogDebug(message string, data DebugInfo) {
	data.FileName, data.LineNumber, data.FunctionName, data.StackTrace = GetDebugInfo(2)
	data.Pid = os.Getpid()
	data.Runtime = runtime.Version()

	entry := l.newLogEntry(message, data)
	entry.Type = Debug
	l.log(entry)
}

// Log logs a generic message as an activity event.
func (l *Logger) Log(message string) {
	l.LogActivity("", message)
}

// ChangePriority changes the priority level of the Logger.
func (l *Logger) ChangePriority(newPriority LogPriority) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.priority = newPriority
}

// Debug2 returns a new Logger with the 'priority' field set to Debug2.
func (l *Logger) Debug2() *Logger {
	return l.WithPriority(Debug2)
}

// Debug1 returns a new Logger with the 'priority' field set to Debug1.
func (l *Logger) Debug1() *Logger {
	return l.WithPriority(Debug1)
}

// Debug0 returns a new Logger with the 'priority' field set to Debug0.
func (l *Logger) Debug0() *Logger {
	return l.WithPriority(Debug0)
}

// Info returns a new Logger with the 'priority' field set to Info.
func (l *Logger) Info() *Logger {
	return l.WithPriority(Info)
}

// Warn returns a new Logger with the 'priority' field set to Warn.
func (l *Logger) Warn() *Logger {
	return l.WithPriority(Warn)
}

// Err returns a new Logger with the 'priority' field set to Err.
func (l *Logger) Err() *Logger {
	return l.WithPriority(Err)
}

// Crit returns a new Logger with the 'priority' field set to Crit.
func (l *Logger) Crit() *Logger {
	return l.WithPriority(Crit)
}

// Sec returns a new Logger with the 'priority' field set to Sec.
func (l *Logger) Sec() *Logger {
	return l.WithPriority(Sec)
}
