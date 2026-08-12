package filter

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	header = `
# Every symbol a filter expression can use, as of the last deliberate
# change. TestEnvSymbolsAreStable fails when one of these disappears.
# Additions are fine and are not listed until this file is regenerated.
`
)

const (
	// The kinds of CEL symbol, which name the golden file's
	// sections and prefix every symbol line listed under them.
	sectionLibrary  = "library"
	sectionFunction = "function"
	sectionMacro    = "macro"
)

var update = flag.Bool("update", false, "update the golden file with the current env symbols")

// goldenSection is a category of symbol in the golden file.
type goldenSection struct {
	name, note string
}

var goldenSections = []goldenSection{
	{
		name: sectionLibrary,
		note: "A version pin only holds if the library reads it.",
	},
	{
		name: sectionFunction,
		note: "Overload identifiers are left out, filters never name them.",
	},
	{
		name: sectionMacro,
		note: "Each is listed with its argument count.",
	},
}

// TestEnvSymbolsAreStable checks that every symbol the filter language had
// when the golden file was written is still present. Users write filters
// in their configuration, so a removed symbol breaks a filter that already
// works, while a new symbol doesn't. The test therefore only looks for what
// is missing, which allows a dependency bump that adds new symbols to pass.
//
// The library version pins cover part of this, but they only work when a
// library reads the version it is given. Regex, IP and CIDR take no version
// at all, so the golden file covers those.
func TestEnvSymbolsAreStable(t *testing.T) {
	var (
		path = filepath.Join("testdata", "symbol.golden")
		have = listSymbols(newTestEnv(t))
		want = readSymbols(t, path)
	)
	for _, symbol := range want {
		if !slices.Contains(have, symbol) {
			kind, name, _ := strings.Cut(symbol, " ")
			t.Errorf("%s %s is gone, which breaks every filter that uses it", name, kind)
		}
	}
	if *update {
		updateGoldenFile(t, path, have, want)
	}
}

// listSymbols lists every symbol a filter expression can use
// from the CEL environment. Overload identifiers are left out
// on purpose because filters never use those.
func listSymbols(e *Env) []string {
	symbols := make([]string, 0, 200)

	for _, lib := range e.env.Libraries() {
		symbols = append(symbols, sectionLibrary+" "+lib)
	}
	for fn := range e.env.Functions() {
		symbols = append(symbols, sectionFunction+" "+fn)
	}
	for _, macro := range e.env.Macros() {
		sig := fmt.Sprintf("%s/%d", macro.Function(), macro.ArgCount())
		symbols = append(symbols, sectionMacro+" "+sig)
	}
	slices.Sort(symbols)

	return slices.Compact(symbols)
}

// updateGoldenFile adds the symbols the environment has to the
// golden file. It only ever adds, so a symbol that went away stays
// listed and keeps failing the test until it is manually removed.
func updateGoldenFile(t *testing.T, path string, have, want []string) {
	t.Helper()

	var (
		all    = slices.Concat(want, have)
		merged = slices.Compact(slices.Sorted(slices.Values(all)))
	)
	var (
		out    strings.Builder
		counts = make([]string, 0, len(goldenSections))
	)
	out.WriteString(strings.TrimLeft(header, "\n"))
	_, _ = fmt.Fprintf(&out, "# %d symbols in total.\n", len(merged))

	for _, section := range goldenSections {
		listed := slices.DeleteFunc(slices.Clone(merged), func(symbol string) bool {
			return !strings.HasPrefix(symbol, section.name+" ")
		})
		counts = append(counts, fmt.Sprintf("%d %s", len(listed), pluralize(section.name)))

		_, _ = fmt.Fprintf(&out, "\n# %d %s. %s\n", len(listed), pluralize(section.name), section.note)

		for _, symbol := range listed {
			out.WriteString(symbol)
			out.WriteString("\n")
		}
	}
	if err := os.WriteFile(path, []byte(out.String()), 0o644); err != nil {
		t.Fatalf("failed to write golden file: %v", err)
	}
	_, _ = fmt.Printf("regenerated %s: %d symbols (%s)\n", path, len(merged), strings.Join(counts, ", "))
}

func readSymbols(t *testing.T, path string) []string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read golden file: %v", err)
	}
	var symbols []string

	for line := range strings.Lines(string(data)) {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			symbols = append(symbols, line)
		}
	}
	return symbols
}

func pluralize(name string) string {
	if after, ok := strings.CutSuffix(name, "y"); ok {
		return after + "ies"
	}
	return name + "s"
}
