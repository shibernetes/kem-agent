package agent

import (
	"errors"
	"log/slog"

	"github.com/shibernetes/kem-agent/config"
	"github.com/shibernetes/kem-agent/filter"
)

// ConfigFailureAttrs returns a configuration failure as log attributes.
//
// A failure with no position, such as one raised outside the document,
// reports the error alone.
func ConfigFailureAttrs(parsed *config.Result, file string, err error) []slog.Attr {
	var filterErr *FilterError

	if errors.As(err, &filterErr) && parsed != nil {
		return filterFailureAttrs(parsed, file, filterErr, err)
	}
	message, line, column := config.Diagnose(err)
	if line == 0 {
		return []slog.Attr{slog.String("error", message)}
	}
	return []slog.Attr{
		slog.String("error", message),
		slog.String("file", file),
		slog.Int("line", line),
		slog.Int("column", column),
	}
}

// ConfigWarningAttrs returns log attributes that describe where a configuration
// warning was raised, as log. The error itself is the log message, so it is
// not repeated in the attributes list.
func ConfigWarningAttrs(file string, warning config.Warning) []slog.Attr {
	line, column := warning.Position()
	if line == 0 {
		return nil
	}
	return []slog.Attr{
		slog.String("file", file),
		slog.Int("line", line),
		slog.Int("column", column),
	}
}

// filterFailureAttrs returns log attributes that describe why and where
// a filter that failed to compile was written in a config document.
//
// The column points into the expression rather than into the document line,
// so the line of the expression it counts into is reported with it.
func filterFailureAttrs(res *config.Result, file string, filterErr *FilterError, err error) []slog.Attr {
	ce, ok := errors.AsType[*filter.CompileError](err)
	if !ok || len(ce.Issues) == 0 {
		return []slog.Attr{
			slog.Any("error", err),
			slog.String("file", file),
			slog.Int("line", res.FilterLine(filterErr.Pipeline, filterErr.Index, 1)),
		}
	}
	var (
		issue = ce.Issues[0]
		attrs = []slog.Attr{
			slog.Any("error", err),
			slog.String("file", file),
			slog.Int("line", res.FilterLine(filterErr.Pipeline, filterErr.Index, issue.Line)),
		}
	)
	// An issue raised once the expression type-checked has no position,
	// so it reports no column.
	if issue.Line > 0 {
		attrs = append(attrs, slog.Int("column", issue.Column+1))
	}
	return append(attrs, slog.String("expression", ce.SourceLine(issue.Line)))
}
