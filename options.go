package ezlog

import (
	"io"
	"log/slog"
	"os"
)

// Format controls the log output format.
type Format int

const (
	// FormatText writes human-readable colored text lines (default).
	FormatText Format = iota
	// FormatJSON writes structured JSON using stdlib slog.JSONHandler.
	FormatJSON
)

// Options configures a Logger.
//
// File rotation is intentionally not built in. Pass a rotating writer
// (e.g. *lumberjack.Logger) as Options.Output instead.
type Options struct {
	// Level is the minimum level to emit. Defaults to slog.LevelInfo.
	Level slog.Level

	// Format selects text or JSON output. Defaults to FormatText.
	Format Format

	// Output is the write destination. Defaults to os.Stderr.
	// When using FormatJSON the stdlib JSONHandler writes to this writer.
	Output io.Writer

	// AddSource includes the caller file and line number in each record.
	AddSource bool

	// NoColor disables ANSI escape codes in FormatText output.
	// Automatically true when Output is not a terminal.
	NoColor bool
}

// DefaultOptions returns a sensible starting configuration.
func DefaultOptions() Options {
	return Options{
		Level:  slog.LevelInfo,
		Format: FormatText,
		Output: os.Stderr,
	}
}
