// Package logx is a tiny leveled logger over the standard library log package.
// Output goes to stderr so that systemd/journald captures it automatically.
package logx

import (
	"log"
	"os"
)

type Logger struct {
	l     *log.Logger
	debug bool
}

func New(debug bool) *Logger {
	return &Logger{
		l:     log.New(os.Stderr, "", log.LstdFlags),
		debug: debug,
	}
}

func (lg *Logger) Debugf(format string, a ...any) {
	if lg.debug {
		lg.l.Printf("[DEBUG] "+format, a...)
	}
}

func (lg *Logger) Infof(format string, a ...any) {
	lg.l.Printf("[INFO]  "+format, a...)
}

func (lg *Logger) Warnf(format string, a ...any) {
	lg.l.Printf("[WARN]  "+format, a...)
}

func (lg *Logger) Errorf(format string, a ...any) {
	lg.l.Printf("[ERROR] "+format, a...)
}
