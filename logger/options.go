/*
Copyright 2021 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package logger

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"
	"time"

	lumberjack "gopkg.in/natefinch/lumberjack.v2"
)

const (
	defaultJSONOutput         = false
	defaultOutputLevel        = "info"
	defaultTimestampFormat    = time.RFC3339Nano
	defaultOutputFileTee      = false
	defaultOutputFileCompress = false
	undefinedAppID            = ""
)

var (
	// logOutputMu protects logOutputCloser from concurrent access.
	logOutputMu     sync.Mutex
	logOutputCloser io.Closer

	// consoleWriter is the console log destination. It is a variable rather
	// than a direct os.Stdout reference so that tests can capture console
	// output.
	consoleWriter io.Writer = os.Stdout
)

// Options defines the sets of options for Dapr logging.
type Options struct {
	// appID is the unique id of Dapr Application
	appID string

	// JSONFormatEnabled is the flag to enable JSON formatted log
	JSONFormatEnabled bool

	// OutputLevel is the level of logging
	OutputLevel string

	// OutputFile is the destination file path for logs.
	OutputFile string

	// TimestampFormat is the format used for log timestamps, expressed as a
	// Go time layout. An empty value means the default (RFC3339 with
	// nanoseconds).
	TimestampFormat string

	// OutputFileTee, when true and OutputFile is set, writes logs to both the
	// file and the console instead of the file only. It has no effect when
	// OutputFile is unset.
	OutputFileTee bool

	// OutputFileMaxSize is the maximum size in megabytes of the log file
	// before it gets rotated. Empty or "0" disables size-based rotation.
	OutputFileMaxSize string

	// OutputFileMaxBackups is the maximum number of rotated log files to keep.
	// Empty or "0" keeps all files, subject to OutputFileMaxAge.
	OutputFileMaxBackups string

	// OutputFileMaxAge is the maximum number of days to retain rotated log
	// files. Empty or "0" disables age-based deletion.
	OutputFileMaxAge string

	// OutputFileCompress, when true, gzip-compresses rotated log files.
	OutputFileCompress bool
}

// SetOutputLevel sets the log output level.
func (o *Options) SetOutputLevel(outputLevel string) error {
	if toLogLevel(outputLevel) == UndefinedLevel {
		return fmt.Errorf("undefined Log Output Level: %s", outputLevel)
	}

	o.OutputLevel = outputLevel

	return nil
}

// SetAppID sets Application ID.
func (o *Options) SetAppID(id string) {
	o.appID = id
}

// AttachCmdFlags attaches log options to command flags.
func (o *Options) AttachCmdFlags(
	stringVar func(p *string, name string, value string, usage string),
	boolVar func(p *bool, name string, value bool, usage string),
) {
	if stringVar != nil {
		stringVar(
			&o.OutputLevel,
			"log-level",
			defaultOutputLevel,
			"Options are debug, info, warn, error, or fatal (default info)")
		stringVar(
			&o.OutputFile,
			"log-file",
			"",
			"Path to a file where logs will be written")
		stringVar(
			&o.TimestampFormat,
			"log-timestamp-format",
			"",
			"Format for log timestamps, expressed as a Go time layout, e.g. '2006/01/02 15:04:05.000' (default RFC3339 with nanoseconds)")
		stringVar(
			&o.OutputFileMaxSize,
			"log-file-max-size",
			"",
			"Maximum size in megabytes of the log file before it gets rotated. 0 disables size-based rotation. No effect without --log-file")
		stringVar(
			&o.OutputFileMaxBackups,
			"log-file-max-backups",
			"",
			"Maximum number of rotated log files to keep. 0 keeps all files. No effect without --log-file")
		stringVar(
			&o.OutputFileMaxAge,
			"log-file-max-age",
			"",
			"Maximum number of days to retain rotated log files. 0 disables age-based deletion. No effect without --log-file")
	}

	if boolVar != nil {
		boolVar(
			&o.JSONFormatEnabled,
			"log-as-json",
			defaultJSONOutput,
			"print log as JSON (default false)")
		boolVar(
			&o.OutputFileTee,
			"log-file-tee",
			defaultOutputFileTee,
			"When --log-file is set, also keep writing logs to the console. No effect without --log-file (default false)")
		boolVar(
			&o.OutputFileCompress,
			"log-file-compress",
			defaultOutputFileCompress,
			"Gzip-compress rotated log files. No effect without --log-file (default false)")
	}
}

// DefaultOptions returns default values of Options.
func DefaultOptions() Options {
	return Options{
		JSONFormatEnabled:  defaultJSONOutput,
		appID:              undefinedAppID,
		OutputLevel:        defaultOutputLevel,
		OutputFile:         "",
		TimestampFormat:    "",
		OutputFileTee:      defaultOutputFileTee,
		OutputFileCompress: defaultOutputFileCompress,
	}
}

// ApplyOptionsToLoggers applys options to all registered loggers.
func ApplyOptionsToLoggers(options *Options) error {
	// optionsLogger reports misconfigurations detected while applying options.
	// It is fetched (or created) before the registry snapshot below so that it
	// is always part of this apply and therefore follows the configured
	// format, level and output like every other logger.
	optionsLogger := NewLogger("dapr.kit.logger")

	internalLoggers := getLoggers()

	// Apply formatting options first
	for _, v := range internalLoggers {
		v.EnableJSONOutput(options.JSONFormatEnabled)

		// Applied via type assertion rather than through the Logger interface,
		// so this stays a non-breaking change for external implementers of
		// Logger. Both in-tree implementations provide the method.
		if s, ok := v.(interface{ SetTimestampFormat(string) }); ok {
			s.SetTimestampFormat(options.TimestampFormat)
		}

		if options.appID != undefinedAppID {
			v.SetAppID(options.appID)
		}
	}

	daprLogLevel := toLogLevel(options.OutputLevel)
	if daprLogLevel == UndefinedLevel {
		return fmt.Errorf("invalid value for --log-level: %s", options.OutputLevel)
	}

	for _, v := range internalLoggers {
		v.SetOutputLevel(daprLogLevel)
	}

	err := setLogOutput(options, internalLoggers)
	if err != nil {
		return err
	}

	if options.OutputFile == "" && (options.OutputFileTee ||
		options.OutputFileCompress ||
		options.OutputFileMaxSize != "" ||
		options.OutputFileMaxBackups != "" ||
		options.OutputFileMaxAge != "") {
		// Warn rather than fail: these options are inert without OutputFile,
		// and an error here would turn a harmless misconfiguration into a
		// startup failure for every binary that attaches these flags.
		optionsLogger.Warn("--log-file-tee, --log-file-max-size, --log-file-max-backups, --log-file-max-age and --log-file-compress have no effect because --log-file is not set")
	}

	return nil
}

// setLogOutput configures log output destination. If options.OutputFile is
// non-empty, logs are written to the file at that path, and additionally to the
// console when options.OutputFileTee is set. If empty, output reverts to the
// console. The new file is opened before closing the previous one so that
// loggers are never left pointing at a closed file descriptor.
func setLogOutput(options *Options, loggers map[string]Logger) error {
	logOutputMu.Lock()
	defer logOutputMu.Unlock()

	var (
		out       = consoleWriter
		newCloser io.Closer
	)

	if options.OutputFile != "" {
		fileOut, closer, err := newFileWriter(options)
		if err != nil {
			return err
		}

		newCloser = closer
		out = fileOut

		if options.OutputFileTee {
			// Console first: io.MultiWriter stops at the first failed writer,
			// so this ordering keeps console output alive even when file
			// writes start failing (e.g. disk full).
			out = io.MultiWriter(consoleWriter, fileOut)
		}
	}

	// Switch all loggers to the new output before closing the old file.
	for _, v := range loggers {
		v.SetOutput(out)
	}

	// Close the previous log file after loggers have been redirected.
	if logOutputCloser != nil {
		logOutputCloser.Close()
	}

	logOutputCloser = newCloser

	return nil
}

// newFileWriter returns the file-backed writer for the options: a plain
// append-mode file when no rotation option is set, or a rotating (lumberjack)
// writer when any rotation option is enabled. The returned io.Closer releases
// the underlying file.
func newFileWriter(options *Options) (io.Writer, io.Closer, error) {
	maxSize, err := parseRotationValue("log-file-max-size", options.OutputFileMaxSize)
	if err != nil {
		return nil, nil, err
	}

	maxBackups, err := parseRotationValue("log-file-max-backups", options.OutputFileMaxBackups)
	if err != nil {
		return nil, nil, err
	}

	maxAge, err := parseRotationValue("log-file-max-age", options.OutputFileMaxAge)
	if err != nil {
		return nil, nil, err
	}

	if maxSize == 0 && maxBackups == 0 && maxAge == 0 && !options.OutputFileCompress {
		f, ferr := os.OpenFile(options.OutputFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if ferr != nil {
			return nil, nil, fmt.Errorf("failed to open log file %q: %w", options.OutputFile, ferr)
		}

		return f, f, nil
	}

	// Pre-create the file with the same permissions as the non-rotating path.
	// lumberjack creates missing files as 0600 and preserves the mode of
	// existing ones, so without this, enabling rotation would silently change
	// new log files from 0644 to 0600 — breaking log shippers that tail the
	// file from another container as a non-owner user.
	f, ferr := os.OpenFile(options.OutputFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if ferr != nil {
		return nil, nil, fmt.Errorf("failed to open log file %q: %w", options.OutputFile, ferr)
	}

	f.Close()

	lj := &lumberjack.Logger{
		Filename:   options.OutputFile,
		MaxSize:    maxSize,    // megabytes; lumberjack defaults to 100 when 0
		MaxBackups: maxBackups, // number of rotated files retained
		MaxAge:     maxAge,     // days
		Compress:   options.OutputFileCompress,
	}

	return lj, lj, nil
}

// parseRotationValue parses a rotation flag value as an unsigned integer. An
// empty value means 0, which disables the corresponding limit. The flag is
// string-typed only because AttachCmdFlags binds through (stringVar, boolVar);
// the value itself is unsigned end-to-end.
func parseRotationValue(name, value string) (int, error) {
	if value == "" {
		return 0, nil
	}

	n, err := strconv.ParseUint(value, 10, 31)
	if err != nil {
		return 0, fmt.Errorf("invalid value for --%s: %q (must be a non-negative integer)", name, value)
	}

	return int(n), nil
}
