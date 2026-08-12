package validate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/goccy/go-yaml/ast"

	"github.com/shibernetes/kem-agent/agent"
	"github.com/shibernetes/kem-agent/config"
	"github.com/shibernetes/kem-agent/config/diag"
	"github.com/shibernetes/kem-agent/filter"
	"github.com/shibernetes/kem-agent/internal/kube"
	"github.com/shibernetes/kem-agent/sink"
)

type severity string

const (
	severityError   severity = "error"
	severityWarning severity = "warning"
)

type result struct {
	Path        string
	Diagnostics []diagnostic
	Pipelines   int
	Sinks       int
	Filters     int
}

// isValid reports whether the configuration is valid.
func (r *result) isValid() bool {
	for _, f := range r.Diagnostics {
		if f.Severity == severityError {
			return false
		}
	}
	return true
}

// A diagnostic represents a problem found in the configuration.
//
// Source contains the original expression for a filter, and the document
// line for anything else. CharColumn indexes into the source, while Line
// always points at the line in the raw config file.
type diagnostic struct {
	Severity   severity
	Code       string
	Message    string
	Path       string
	Line       int
	CharColumn int
	Length     int
	Source     string
	Secondary  []annotation
}

type annotation struct {
	CharColumn int
	Length     int
	Label      string
}

// analyze checks the configuration by replicating the startup sequence
// of the agent. It builds every component, and releases them immediately.
func analyze(path string, data []byte, factories sink.Factories) result {
	var (
		lines = strings.Split(string(data), "\n")
		res   = result{Path: path}
	)
	parsed, err := config.Parse(data, factories)
	if parsed != nil {
		res.Diagnostics = append(res.Diagnostics, warningDiagnostics(lines, parsed)...)
	}
	if err != nil {
		res.Diagnostics = append(res.Diagnostics, documentDiagnostic(lines, err))
		return res
	}
	res.Sinks = len(parsed.Config.Sinks)
	res.Pipelines = len(parsed.Config.Pipelines)

	for _, pipeline := range parsed.Config.Pipelines {
		res.Filters += len(pipeline.Filters)
	}
	// Building the agent proves the configuration can run, since it
	// compiles filters and decodes the sink's settings.
	a, err := agent.New(parsed.Config, agent.Options{
		Factories: factories,
		Logger:    slog.New(slog.DiscardHandler),
		Kube:      kube.OfflineConfig(),
	})
	if err != nil {
		res.Diagnostics = append(res.Diagnostics, buildDiagnostics(parsed, err)...)
		return res
	}
	_ = a.Close(context.Background())
	return res
}

// documentDiagnostic positions a config file parse failure.
//
// Only our own errors wrap a node, so only those have a document path.
// The decoder's errors only report a token and nothing more.
func documentDiagnostic(lines []string, err error) diagnostic {
	message, line, column := config.Diagnose(err)

	found := diagnostic{
		Severity:   severityError,
		Message:    message,
		Line:       line,
		CharColumn: column,
		Length:     config.Span(err),
		Source:     sourceLine(lines, line),
	}
	if de, ok := errors.AsType[*diag.Error](err); ok && de.Node() != nil {
		found.Path = de.Node().GetPath()
	}
	return found
}

// buildDiagnostics positions a build failure.
//
// A filter compilation error is the only one reported with a position
// at runtime, which the CEL compiler indexes relative to the expression.
// An expression written over several lines gets one diagnostic for each
// line that contains an issue, so every caret points into the line shown
// above it.
func buildDiagnostics(parsed *config.Result, err error) []diagnostic {
	fe, ok := errors.AsType[*agent.FilterError](err)
	if !ok {
		return []diagnostic{{Severity: severityError, Message: err.Error()}}
	}
	ce, ok := errors.AsType[*filter.CompileError](err)
	if !ok || len(ce.Issues) == 0 {
		return []diagnostic{{
			Severity: severityError,
			Code:     "cel",
			Message:  err.Error(),
			Path:     filterPath(fe.Pipeline, fe.Index),
			Line:     parsed.FilterLine(fe.Pipeline, fe.Index, 1),
		}}
	}
	var (
		found  = make([]diagnostic, 0, len(ce.Issues))
		byLine = make(map[int]int)
	)
	for _, issue := range ce.Issues {
		// The first issue on a line starts the diagnostic, and the
		// others on that line are annotated beside it.
		if i, ok := byLine[issue.Line]; ok {
			if issue.Line > 0 {
				found[i].Secondary = append(found[i].Secondary, annotation{
					CharColumn: issue.Column + 1,
					Length:     issue.Length,
					Label:      issue.Message,
				})
			}
			continue
		}
		byLine[issue.Line] = len(found)

		d := diagnostic{
			Severity: severityError,
			Code:     "cel",
			Message:  issue.Message,
			Path:     filterPath(fe.Pipeline, fe.Index),
			Line:     parsed.FilterLine(fe.Pipeline, fe.Index, issue.Line),
			Source:   ce.SourceLine(issue.Line),
		}
		if issue.Line > 0 {
			d.CharColumn = issue.Column + 1
			d.Length = issue.Length
		}
		found = append(found, d)
	}
	return found
}

// warningDiagnostics positions every warning a parsed configuration raises.
func warningDiagnostics(lines []string, res *config.Result) []diagnostic {
	var found []diagnostic

	for _, warning := range agent.ConfigWarnings(res) {
		found = append(found, at(warning.Node, lines, diagnostic{
			Severity: severityWarning,
			Message:  warning.Message,
		}))
	}
	return found
}

// at positions a diagnostic at a document node.
func at(node ast.Node, lines []string, d diagnostic) diagnostic {
	if node == nil {
		return d
	}
	token := node.GetToken()
	if token == nil {
		return d
	}
	d.Line, d.CharColumn = token.Position.Line, token.Position.Column
	d.Path = node.GetPath()
	d.Source = sourceLine(lines, d.Line)

	return d
}

// filterPath returns the path of a pipeline's filter.
func filterPath(pipeline string, index int) string {
	return fmt.Sprintf("$.pipelines.%s.filters[%d]", pipeline, index)
}

func sourceLine(lines []string, idx int) string {
	if idx < 1 || idx > len(lines) {
		return ""
	}
	return strings.TrimSuffix(lines[idx-1], "\r")
}
