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
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	lumberjack "gopkg.in/natefinch/lumberjack.v2"
)

func TestOptions(t *testing.T) {
	t.Run("default options", func(t *testing.T) {
		o := DefaultOptions()
		assert.Equal(t, defaultJSONOutput, o.JSONFormatEnabled)
		assert.Equal(t, undefinedAppID, o.appID)
		assert.Equal(t, defaultOutputLevel, o.OutputLevel)
		assert.Empty(t, o.OutputFile)
		assert.Equal(t, defaultOutputFileTee, o.OutputFileTee)
	})

	t.Run("set dapr ID", func(t *testing.T) {
		o := DefaultOptions()
		assert.Equal(t, undefinedAppID, o.appID)

		o.SetAppID("dapr-app")
		assert.Equal(t, "dapr-app", o.appID)
	})

	t.Run("attaching log related cmd flags", func(t *testing.T) {
		o := DefaultOptions()

		logLevelAsserted := false
		logFileAsserted := false
		logTimestampFormatAsserted := false
		testStringVarFn := func(p *string, name string, value string, usage string) {
			if name == "log-level" && value == defaultOutputLevel {
				logLevelAsserted = true
			}

			if name == "log-file" && value == "" {
				logFileAsserted = true
			}

			if name == "log-timestamp-format" && value == "" {
				logTimestampFormatAsserted = true
			}
		}

		logAsJSONAsserted := false
		logFileTeeAsserted := false
		testBoolVarFn := func(p *bool, name string, value bool, usage string) {
			if name == "log-as-json" && value == defaultJSONOutput {
				logAsJSONAsserted = true
			}

			if name == "log-file-tee" && value == defaultOutputFileTee {
				logFileTeeAsserted = true
			}
		}

		o.AttachCmdFlags(testStringVarFn, testBoolVarFn)

		// assert
		assert.True(t, logLevelAsserted)
		assert.True(t, logFileAsserted)
		assert.True(t, logTimestampFormatAsserted)
		assert.True(t, logAsJSONAsserted)
		assert.True(t, logFileTeeAsserted)
	})
}

func TestApplyOptionsToLoggers(t *testing.T) {
	testOptions := Options{
		JSONFormatEnabled: true,
		appID:             "dapr-app",
		OutputLevel:       "debug",
		TimestampFormat:   "2006/01/02 15:04:05.000",
	}

	// Create two loggers
	testLoggers := []Logger{
		NewLogger("testLogger0"),
		NewLogger("testLogger1"),
	}

	for _, l := range testLoggers {
		l.EnableJSONOutput(false)
		l.SetOutputLevel(InfoLevel)
	}

	require.NoError(t, ApplyOptionsToLoggers(&testOptions))

	for _, l := range testLoggers {
		assert.Equal(
			t,
			"dapr-app",
			(l.(*daprLogger)).logger.Data[logFieldAppID])
		assert.Equal(
			t,
			toLogrusLevel(DebugLevel),
			(l.(*daprLogger)).logger.Logger.GetLevel())
		assert.Equal(
			t,
			"2006/01/02 15:04:05.000",
			(l.(*daprLogger)).timestampFormat)
	}
}

func TestApplyOptionsToLoggersFileOutput(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "dapr.log")

	testOptions := Options{
		OutputLevel: "debug",
		OutputFile:  logPath,
	}

	l := NewLogger("testLoggerFileOutput")

	require.NoError(t, ApplyOptionsToLoggers(&testOptions))
	t.Cleanup(func() {
		// Revert to stdout, which also closes the log file.
		require.NoError(t, ApplyOptionsToLoggers(&Options{
			OutputLevel: "info",
		}))
	})

	dl, ok := l.(*daprLogger)
	require.True(t, ok)
	fileOut, ok := dl.logger.Logger.Out.(*os.File)
	require.True(t, ok)
	assert.Equal(t, logPath, fileOut.Name())

	msg := "log-file-test-message"
	l.Info(msg)

	b, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.Contains(t, string(b), msg)
}

func TestLogFileTee(t *testing.T) {
	// t.TempDir() must be called before the cleanup below is registered.
	// Cleanups run LIFO, so registering TempDir's RemoveAll first makes it run
	// last — after the cleanup that closes the log file. The reverse order
	// fails on Windows, which cannot delete a file that is still open.
	logPath := filepath.Join(t.TempDir(), "dapr.log")

	var console bytes.Buffer

	consoleWriter = &console

	t.Cleanup(func() {
		consoleWriter = os.Stdout
		// Re-point all registered loggers back at the real stdout, which also
		// closes the log file. Doing this before restoring consoleWriter would
		// leave them aimed at the dead test buffer.
		o := DefaultOptions()
		require.NoError(t, ApplyOptionsToLoggers(&o))
	})

	o := DefaultOptions()
	o.OutputFile = logPath
	o.OutputFileTee = true

	l := NewLogger("testLoggerTee")

	require.NoError(t, ApplyOptionsToLoggers(&o))

	msg := "hello-tee"
	l.Info(msg)

	b, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.Contains(t, string(b), msg, "message should reach the file")
	assert.Contains(t, console.String(), msg, "message should also reach the console")
}

func TestLogFileTeeDisabledKeepsFileOnly(t *testing.T) {
	// TempDir before Cleanup — see the ordering note in TestLogFileTee.
	logPath := filepath.Join(t.TempDir(), "dapr.log")

	var console bytes.Buffer

	consoleWriter = &console

	t.Cleanup(func() {
		consoleWriter = os.Stdout
		o := DefaultOptions()
		require.NoError(t, ApplyOptionsToLoggers(&o))
	})

	o := DefaultOptions()
	o.OutputFile = logPath
	// OutputFileTee deliberately left false — this is the pre-existing
	// behaviour and must not change.

	l := NewLogger("testLoggerTeeDisabled")

	require.NoError(t, ApplyOptionsToLoggers(&o))

	msg := "file-only"
	l.Info(msg)

	b, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.Contains(t, string(b), msg)
	assert.NotContains(t, console.String(), msg, "console must stay silent when tee is off")
}

func TestNewFileWriter(t *testing.T) {
	t.Run("rotation options build a rotating writer", func(t *testing.T) {
		o := DefaultOptions()
		o.OutputFile = filepath.Join(t.TempDir(), "dapr.log")
		o.OutputFileMaxSize = "1"
		o.OutputFileMaxBackups = "3"
		o.OutputFileMaxAge = "7"
		o.OutputFileCompress = true

		w, c, err := newFileWriter(&o)
		require.NoError(t, err)

		lj, ok := w.(*lumberjack.Logger)
		require.True(t, ok, "expected a lumberjack writer")
		assert.Equal(t, o.OutputFile, lj.Filename)
		assert.Equal(t, 1, lj.MaxSize)
		assert.Equal(t, 3, lj.MaxBackups)
		assert.Equal(t, 7, lj.MaxAge)
		assert.True(t, lj.Compress)

		require.NoError(t, c.Close())
	})

	t.Run("no rotation options keeps a plain append file", func(t *testing.T) {
		o := DefaultOptions()
		o.OutputFile = filepath.Join(t.TempDir(), "dapr.log")

		w, c, err := newFileWriter(&o)
		require.NoError(t, err)

		_, ok := w.(*os.File)
		assert.True(t, ok, "expected a plain *os.File when no rotation option is set")

		require.NoError(t, c.Close())
	})

	t.Run("compress alone is enough to enable rotation", func(t *testing.T) {
		o := DefaultOptions()
		o.OutputFile = filepath.Join(t.TempDir(), "dapr.log")
		o.OutputFileCompress = true

		w, c, err := newFileWriter(&o)
		require.NoError(t, err)

		_, ok := w.(*lumberjack.Logger)
		assert.True(t, ok)

		require.NoError(t, c.Close())
	})

	t.Run("invalid rotation value errors", func(t *testing.T) {
		for _, tc := range []struct {
			name  string
			apply func(o *Options)
		}{
			{"max-size not a number", func(o *Options) { o.OutputFileMaxSize = "not-a-number" }},
			{"max-backups negative", func(o *Options) { o.OutputFileMaxBackups = "-1" }},
			{"max-age not a number", func(o *Options) { o.OutputFileMaxAge = "7d" }},
		} {
			t.Run(tc.name, func(t *testing.T) {
				o := DefaultOptions()
				o.OutputFile = filepath.Join(t.TempDir(), "dapr.log")
				tc.apply(&o)

				_, _, err := newFileWriter(&o)
				require.Error(t, err)
			})
		}
	})
}

func TestParseRotationValue(t *testing.T) {
	t.Run("empty means disabled", func(t *testing.T) {
		got, err := parseRotationValue("log-file-max-size", "")
		require.NoError(t, err)
		assert.Equal(t, 0, got)
	})

	t.Run("parses a non-negative integer", func(t *testing.T) {
		got, err := parseRotationValue("log-file-max-size", "42")
		require.NoError(t, err)
		assert.Equal(t, 42, got)
	})

	t.Run("rejects negatives and non-numbers", func(t *testing.T) {
		for _, v := range []string{"-1", "abc", "1.5", " 1"} {
			_, err := parseRotationValue("log-file-max-size", v)
			require.Error(t, err, "value %q should be rejected", v)
		}
	})
}

func TestApplyOptionsToLoggersRotation(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "dapr.log")

	o := DefaultOptions()
	o.OutputFile = logPath
	o.OutputFileMaxSize = "1"

	l := NewLogger("testLoggerRotation")

	require.NoError(t, ApplyOptionsToLoggers(&o))

	t.Cleanup(func() {
		d := DefaultOptions()
		require.NoError(t, ApplyOptionsToLoggers(&d))
	})

	msg := "rotating-message"
	l.Info(msg)

	b, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.Contains(t, string(b), msg)
}

func TestApplyOptionsToLoggersFileOutputReapply(t *testing.T) {
	dir := t.TempDir()
	logPath1 := filepath.Join(dir, "dapr1.log")
	logPath2 := filepath.Join(dir, "dapr2.log")

	l := NewLogger("testLoggerReapply")

	t.Cleanup(func() {
		require.NoError(t, ApplyOptionsToLoggers(&Options{
			OutputLevel: "info",
		}))
	})

	// Apply first file output.
	require.NoError(t, ApplyOptionsToLoggers(&Options{
		OutputLevel: "debug",
		OutputFile:  logPath1,
	}))
	l.Info("message-one")

	// Re-apply with a different file — should close the first.
	require.NoError(t, ApplyOptionsToLoggers(&Options{
		OutputLevel: "debug",
		OutputFile:  logPath2,
	}))
	l.Info("message-two")

	b1, err := os.ReadFile(logPath1)
	require.NoError(t, err)
	assert.Contains(t, string(b1), "message-one")
	assert.NotContains(t, string(b1), "message-two")

	b2, err := os.ReadFile(logPath2)
	require.NoError(t, err)
	assert.Contains(t, string(b2), "message-two")
}
