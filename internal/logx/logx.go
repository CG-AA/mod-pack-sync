// Package logx provides localized terminal output that is also teed to a
// timestamped log file the user can copy-paste to support.
package logx

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/cg-aa/mod-pack-sync/internal/i18n"
)

// Logger writes to stdout and a log file simultaneously.
type Logger struct {
	t       *i18n.T
	file    *os.File
	w       io.Writer
	logPath string
}

// New creates a Logger writing to <stateDir>/logs/<timestamp>.txt. If the log
// file cannot be created, output still goes to stdout.
func New(stateDir string, t *i18n.T) *Logger {
	l := &Logger{t: t, w: os.Stdout}
	logDir := filepath.Join(stateDir, "logs")
	if err := os.MkdirAll(logDir, 0o755); err == nil {
		p := filepath.Join(logDir, time.Now().Format("20060102-150405")+".txt")
		if f, ferr := os.Create(p); ferr == nil {
			l.file = f
			l.logPath = p
			l.w = io.MultiWriter(os.Stdout, f)
		}
	}
	return l
}

// LogPath returns the path of the log file (may be empty).
func (l *Logger) LogPath() string { return l.logPath }

// Close closes the underlying log file.
func (l *Logger) Close() {
	if l.file != nil {
		l.file.Close()
	}
}

// Say prints a translated, formatted line.
func (l *Logger) Say(key string, args ...any) {
	fmt.Fprintln(l.w, l.t.F(key, args...))
}

// Raw prints a line that is not a catalog key (e.g. a wormhole code).
func (l *Logger) Raw(s string) { fmt.Fprintln(l.w, s) }

// Progress renders a single-line byte progress indicator to stdout only (it is
// not teed, to avoid spamming the log file).
func (l *Logger) Progress(sent, total int64) {
	if total <= 0 {
		fmt.Fprintf(os.Stdout, "\r  %d bytes", sent)
		return
	}
	fmt.Fprintf(os.Stdout, "\r  %3d%% (%d/%d)", sent*100/total, sent, total)
	if sent >= total {
		fmt.Fprintln(os.Stdout)
	}
}

// Fatal logs a localized error-with-log-path message and exits non-zero. On
// Windows it pauses first so a double-clicked window does not vanish.
func (l *Logger) Fatal(err error) {
	fmt.Fprintf(l.w, "ERROR: %v\n", err)
	if l.logPath != "" {
		l.Say("error_logged", l.logPath)
	}
	l.PauseIfWindows()
	l.Close()
	os.Exit(1)
}

// Prompt prints a translated prompt and reads one line of input.
func (l *Logger) Prompt(key string) string {
	fmt.Fprint(os.Stdout, l.t.S(key))
	if l.file != nil {
		fmt.Fprintln(l.file, l.t.S(key))
	}
	sc := bufio.NewScanner(os.Stdin)
	if sc.Scan() {
		return sc.Text()
	}
	return ""
}

// PauseIfWindows waits for Enter before returning, but only on Windows, so the
// console window from a double-click stays open long enough to read.
func (l *Logger) PauseIfWindows() {
	if runtime.GOOS != "windows" {
		return
	}
	fmt.Fprintln(os.Stdout, l.t.S("press_enter_exit"))
	bufio.NewScanner(os.Stdin).Scan()
}
