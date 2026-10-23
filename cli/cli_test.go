// This file is part of the Go port of Twig.
//
// (c) Fabien Potencier
//
// For the full copyright and license information, please view the LICENSE
// file that was distributed with this source code.

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testCase is one command line run: the files of its working directory, the arguments, the standard input and the
// output, error output and exit status the reference implementation produced.
type testCase struct {
	ID       string            `json:"id"`
	Files    map[string]string `json:"files"`
	Argv     []string          `json:"argv"`
	Stdin    string            `json:"stdin"`
	Expected struct {
		Out  string `json:"out"`
		Err  string `json:"err"`
		Code int    `json:"code"`
	} `json:"expected"`
}

func TestCommandAgainstRecordedCases(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "testdata", "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []testCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)
	failed := 0
	for _, c := range cases {
		dir := t.TempDir()
		for name, content := range c.Files {
			path := filepath.Join(dir, name)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		code := Run(c.Argv, strings.NewReader(c.Stdin), &stdout, &stderr)
		if stdout.String() != c.Expected.Out || stderr.String() != c.Expected.Err || code != c.Expected.Code {
			failed++
			if failed <= 5 {
				t.Errorf("%s: got (%d, %q, %q), want (%d, %q, %q)", c.ID, code, stdout.String(), stderr.String(),
					c.Expected.Code, c.Expected.Out, c.Expected.Err)
			}
		}
	}
	if failed > 5 {
		t.Errorf("%d of %d cases failed", failed, len(cases))
	}
}

func TestUsageErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--bogus"}, strings.NewReader(""), &stdout, &stderr); code != 64 {
		t.Errorf("exit status %d, want 64", code)
	}
	want := "twig: unknown option \"--bogus\"\n" + usage
	if stderr.String() != want || stdout.Len() != 0 {
		t.Errorf("got %q, want %q", stderr.String(), want)
	}
}
