package validate

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

const (
	outputText = "text"
	outputJSON = "json"
)

const (
	colorAuto   = "auto"
	colorAlways = "always"
	colorOff    = "off"
)

// The palette uses base 16 colors rather than hexadecimal ones,
// so the output follows the operator's terminal theme.
var (
	boldStyle   = lipgloss.NewStyle().Bold(true)
	dimStyle    = lipgloss.NewStyle().Faint(true)
	gutterStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
	caretStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	validStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	warnStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("3"))
	errorStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1"))
)

type jsonDiagnostic struct {
	Severity   string           `json:"severity"`
	Code       string           `json:"code,omitempty"`
	Message    string           `json:"message"`
	File       string           `json:"file"`
	Path       string           `json:"path,omitempty"`
	Line       int              `json:"line,omitempty"`
	CharColumn int              `json:"char_column,omitempty"`
	Length     int              `json:"length,omitempty"`
	Source     string           `json:"source,omitempty"`
	Secondary  []jsonAnnotation `json:"secondary,omitempty"`
}

type jsonAnnotation struct {
	CharColumn int    `json:"char_column"`
	Length     int    `json:"length,omitempty"`
	Label      string `json:"label"`
}

func validateOutputFlag(output string) error {
	switch output {
	case outputText, outputJSON:
		return nil
	default:
		return fmt.Errorf("invalid output format %q, allowed values are %s and %s", output, outputText, outputJSON)
	}
}

func validateColorFlag(color string) error {
	switch color {
	case colorAuto, colorAlways, colorOff:
		return nil
	default:
		return fmt.Errorf("invalid color %q, allowed values are %s, %s and %s",
			color, colorAuto, colorAlways, colorOff)
	}
}

func render(w io.Writer, res *result, output, color string) error {
	if output == outputJSON {
		return renderJSON(w, res)
	}
	renderText(colorWriter(w, color), res)

	return nil
}

// colorWriter wraps w so the escape sequences a lipgloss style renders
// are downgraded to the color profile the destination supports, and
// stripped where it supports none. A style renders them whatever the
// destination is, so the profile is what decides. Only auto reads the
// environment to pick one.
func colorWriter(w io.Writer, color string) io.Writer {
	switch color {
	case colorAlways:
		return &colorprofile.Writer{Forward: w, Profile: colorprofile.ANSI}
	case colorOff:
		return &colorprofile.Writer{Forward: w, Profile: colorprofile.NoTTY}
	default:
		return colorprofile.NewWriter(w, os.Environ())
	}
}

// renderJSON writes JSON-formatted diagnostics to w.
func renderJSON(w io.Writer, res *result) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)

	for _, f := range res.Diagnostics {
		secondary := make([]jsonAnnotation, len(f.Secondary))
		for i, s := range f.Secondary {
			secondary[i] = jsonAnnotation(s)
		}
		err := enc.Encode(jsonDiagnostic{
			Severity:   string(f.Severity),
			Code:       f.Code,
			Message:    f.Message,
			File:       res.Path,
			Path:       f.Path,
			Line:       f.Line,
			CharColumn: f.CharColumn,
			Length:     f.Length,
			Source:     f.Source,
			Secondary:  secondary,
		})
		if err != nil {
			return fmt.Errorf("failed to encode diagnostic: %w", err)
		}
	}
	return nil
}

// renderText writes text-formatted diagnostics to w.
func renderText(w io.Writer, res *result) {
	for _, f := range res.Diagnostics {
		renderRustcBlock(w, res.Path, f)
	}
	if !res.isValid() {
		return
	}
	summary := fmt.Sprintf("— %s, %s, %s%s",
		plural(res.Pipelines, "pipeline"),
		plural(res.Sinks, "sink"),
		plural(res.Filters, "filter"),
		warningCount(res),
	)
	_, _ = fmt.Fprintf(w, "%s %s\n",
		validStyle.Render(res.Path+" is valid"), dimStyle.Render(summary))
}

// renderRustcBlock writes a diagnostic block, in the shape rustc uses.
//
//	error[cel]: undefined field 'foo'
//	  --> config.yaml:11:7 · $.pipelines.archive.filters[1]
//	   |
//	11 | event.foo == 'Warning'
//	   |       ^^^
func renderRustcBlock(w io.Writer, file string, f diagnostic) {
	style, level := errorStyle, string(severityError)

	if f.Severity == severityWarning {
		style, level = warnStyle, string(severityWarning)
	}
	var code string
	if f.Code != "" {
		code = dimStyle.Render("[" + f.Code + "]")
	}
	_, _ = fmt.Fprintf(w, "%s%s%s\n", style.Render(level), code, boldStyle.Render(": "+f.Message))

	origin := file
	switch {
	case f.Line > 0 && f.CharColumn > 0:
		origin = fmt.Sprintf("%s:%d:%d", file, f.Line, f.CharColumn)
	case f.Line > 0:
		origin = fmt.Sprintf("%s:%d", file, f.Line)
	}
	var configPath string
	if f.Path != "" {
		configPath = dimStyle.Render(" · " + f.Path)
	}
	// The arrow is indented by the gutter, so it points at the block
	// from the column left of the bar, which is where rustc puts it.
	_, _ = fmt.Fprintf(w, "%s%s %s%s\n",
		strings.Repeat(" ", len(lineNumber(f.Line))), gutterStyle.Render("-->"), origin, configPath)

	if f.Source != "" {
		renderSource(w, f)
	}
	_, _ = fmt.Fprintln(w)
}

// renderSource writes the line a diagnostic came from, with a caret
// under each position where an issue has been identified.
func renderSource(w io.Writer, f diagnostic) {
	number := lineNumber(f.Line)
	gutter := gutterStyle.Render(strings.Repeat(" ", len(number)) + " |")

	_, _ = fmt.Fprintln(w, gutter)
	_, _ = fmt.Fprintf(w, "%s %s\n", gutterStyle.Render(number+" |"), f.Source)

	if row := caretRow(f); row != "" {
		_, _ = fmt.Fprintf(w, "%s %s\n", gutter, caretStyle.Render(row))
	}
	// The header prints the first message, so the rest are noted
	// under the carets that point at them.
	for _, s := range f.Secondary {
		_, _ = fmt.Fprintf(w, "%s %s\n",
			gutterStyle.Render(strings.Repeat(" ", len(number))+" ="), s.Label)
	}
}

// caretRow draws a caret under every position of a diagnostic.
func caretRow(f diagnostic) string {
	var row []rune

	annotations := slices.Concat([]annotation{{CharColumn: f.CharColumn, Length: f.Length}}, f.Secondary)
	for _, s := range annotations {
		if s.CharColumn <= 0 {
			continue
		}
		width := max(s.Length, 1)
		for len(row) < s.CharColumn-1+width {
			row = append(row, ' ')
		}
		for i := range width {
			row[s.CharColumn-1+i] = '^'
		}
	}
	return string(row)
}

// lineNumber returns the line number for the gutter, or a
// single space when the diagnostic names no line.
func lineNumber(line int) string {
	if line <= 0 {
		return " "
	}
	return strconv.Itoa(line)
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

func warningCount(res *result) string {
	var n int
	for _, f := range res.Diagnostics {
		if f.Severity == severityWarning {
			n++
		}
	}
	if n == 0 {
		return ""
	}
	return ", " + plural(n, "warning")
}
