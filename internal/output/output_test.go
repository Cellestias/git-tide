package output

import (
	"bytes"
	"os"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TestPlainByDefault(t *testing.T) {
	var out, errOut bytes.Buffer
	p := Printer{Out: &out, Err: &errOut}
	p.Step("a %d", 1)
	p.Info("b")
	p.Success("c")
	p.Line("d")
	p.Warn("e")
	p.Fail("f")
	p.Note("g")
	if want := "→ a 1\n  b\n✓ c\nd\n"; out.String() != want {
		t.Errorf("out = %q, want %q", out.String(), want)
	}
	if want := "! e\n✗ f\n→ g\n"; errOut.String() != want {
		t.Errorf("err = %q, want %q", errOut.String(), want)
	}
	if p.Green("x") != "x" || p.Bold("x") != "x" || p.DimErr("x") != "x" {
		t.Error("styles changed text with colour off")
	}
}

func TestColoursSymbolsPerStream(t *testing.T) {
	var out, errOut bytes.Buffer
	p := Printer{Out: &out, Err: &errOut, ColorOut: true} // only stdout is a terminal
	p.Success("done")
	p.Fail("broken")
	if !strings.Contains(out.String(), "\x1b[32m✓\x1b[0m done") {
		t.Errorf("success not green: %q", out.String())
	}
	if strings.Contains(errOut.String(), "\x1b") {
		t.Errorf("stderr coloured although it is not a terminal: %q", errOut.String())
	}
	p.ColorErr = true
	errOut.Reset()
	p.Fail("broken")
	p.Warn("careful")
	if !strings.Contains(errOut.String(), "\x1b[31m✗\x1b[0m broken") || !strings.Contains(errOut.String(), "\x1b[33m!\x1b[0m careful") {
		t.Errorf("stderr = %q", errOut.String())
	}
	if got := p.Green(""); got != "" {
		t.Errorf("empty text got codes: %q", got)
	}
}

func TestColorEnabled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("colours are off on Windows")
	}
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	tty, err := os.Open("/dev/null") // a character device, like a terminal for this check
	if err != nil {
		t.Skip(err)
	}
	defer tty.Close()

	cases := []struct {
		name string
		w    *os.File
		env  map[string]string
		want bool
	}{
		{"terminal", tty, nil, true},
		{"NO_COLOR wins", tty, map[string]string{"NO_COLOR": "1"}, false},
		{"dumb terminal", tty, map[string]string{"TERM": "dumb"}, false},
		{"force on a non-terminal", nil, map[string]string{"CLICOLOR_FORCE": "1"}, true},
		{"force 0 is off", nil, map[string]string{"CLICOLOR_FORCE": "0"}, false},
		{"NO_COLOR beats force", nil, map[string]string{"NO_COLOR": "1", "CLICOLOR_FORCE": "1"}, false},
		{"non-terminal", nil, nil, false},
	}
	for _, tc := range cases {
		var w interface{ Write([]byte) (int, error) } = &bytes.Buffer{}
		if tc.w != nil {
			w = tc.w
		}
		if got := ColorEnabled(w, env(tc.env)); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestTableAlignsWithAndWithoutColour(t *testing.T) {
	rows := [][]string{{"ID", "TITLE", "STATE"}, {"#1", "Añadir login", "open"}, {"#22", "x", "merged"}}
	render := func(color bool) string {
		var out bytes.Buffer
		p := Printer{Out: &out, ColorOut: color}
		p.Table(rows, func(_, col int, c string) string {
			if col == 2 {
				return p.Green(c)
			}
			return c
		})
		return out.String()
	}
	plain := render(false)
	want := "ID   TITLE         STATE\n#1   Añadir login  open\n#22  x             merged\n"
	if plain != want {
		t.Errorf("plain =\n%q\nwant\n%q", plain, want)
	}
	coloured := render(true)
	if !strings.Contains(coloured, "\x1b[32mopen\x1b[0m") || !strings.Contains(coloured, "\x1b[1mID\x1b[0m") {
		t.Errorf("no colours in %q", coloured)
	}
	if ansi.ReplaceAllString(coloured, "") != plain {
		t.Errorf("colour changed the layout:\n%q\nvs\n%q", ansi.ReplaceAllString(coloured, ""), plain)
	}
}
