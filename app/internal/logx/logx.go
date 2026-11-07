// Package logx provides a tiny, greppable progress logger.
//
// Every line looks like:
//
//	12:04:11 STEP  dashboard  [3/29] [APM Dashboard] (febljk0a32qyoa)
//
// The fixed "LEVEL  section" prefix makes the output easy to scan by eye and
// easy to filter with grep.
package logx

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"
)

// Logger writes human readable progress lines.
type Logger struct {
	inner *log.Logger
}

// New returns a Logger writing to w.
func New(w io.Writer) *Logger {
	return &Logger{inner: log.New(w, "", log.Ltime)}
}

// Default returns a Logger writing to stdout.
func Default() *Logger {
	return New(os.Stdout)
}

func (l *Logger) write(level, section, format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	if section != "" {
		l.inner.Printf("%-5s %-10s %s", level, section, msg)
		return
	}
	l.inner.Printf("%-5s %s", level, msg)
}

// Step logs the beginning of a new unit of work.
func (l *Logger) Step(section, format string, a ...any) {
	l.write("STEP", section, format, a...)
}

// Info logs a normal progress message.
func (l *Logger) Info(section, format string, a ...any) {
	l.write("INFO", section, format, a...)
}

// Warn logs something suspicious that did not stop the run.
func (l *Logger) Warn(section, format string, a ...any) {
	l.write("WARN", section, format, a...)
}

// Error logs a failure.
func (l *Logger) Error(section, format string, a ...any) {
	l.write("ERROR", section, format, a...)
}

// Section returns a logger that prefixes every message with the given section.
func (l *Logger) Section(section string) *Section {
	return &Section{log: l, name: section}
}

// Section is a Logger bound to a fixed section name.
type Section struct {
	log  *Logger
	name string
}

// Step logs the beginning of a new unit of work.
func (s *Section) Step(format string, a ...any) { s.log.write("STEP", s.name, format, a...) }

// Info logs a normal progress message.
func (s *Section) Info(format string, a ...any) { s.log.write("INFO", s.name, format, a...) }

// Warn logs something suspicious that did not stop the run.
func (s *Section) Warn(format string, a ...any) { s.log.write("WARN", s.name, format, a...) }

// Error logs a failure.
func (s *Section) Error(format string, a ...any) { s.log.write("ERROR", s.name, format, a...) }

// Truncate shortens s to at most n runes, appending an ellipsis when cut.
func Truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

// OneLine collapses a multi line string (a query, an error) into a single line
// so it stays readable inside a log line.
func OneLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	return strings.Join(strings.Fields(s), " ")
}
