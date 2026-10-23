// This file is part of the Go port of Twig.
//
// (c) Fabien Potencier
//
// For the full copyright and license information, please view the LICENSE
// file that was distributed with this source code.

// Package cli implements the twig command line: options, the JSON context, rendering and error reporting.
package cli

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/twigphp/twig"
	"github.com/twigphp/twig/internal/php"
)

const usage = "usage: twig [options] TEMPLATE_DIR TEMPLATE_NAME [CONTEXT_FILE]\n"

// exitError carries a message and an exit status.
type exitError struct {
	msg    string
	status int
	usage  bool
}

func (e *exitError) Error() string { return e.msg }

func usageError(format string, args ...any) *exitError {
	return &exitError{msg: fmt.Sprintf(format, args...), status: 64, usage: true}
}

func fail(status int, format string, args ...any) *exitError {
	return &exitError{msg: fmt.Sprintf(format, args...), status: status}
}

type pair struct{ name, value string }

type options struct {
	autoescape                        string
	strict                            bool
	timezone, dateFormat, intervalFmt *string
	decimals, decimalPoint, thousands *string
	namespaces, globals               []pair
	block                             *string
}

var valued = map[string]string{
	"--autoescape": "autoescape", "--timezone": "timezone", "--date-format": "date_format",
	"--interval-format": "interval_format", "--decimals": "decimals", "--decimal-point": "decimal_point",
	"--thousands-sep": "thousands_sep", "--namespace": "namespaces", "--block": "block", "--global": "globals",
}

var strategies = []string{"html", "js", "css", "url", "html_attr", "name", "false"}

var decimalsRe = regexp.MustCompile(`^(0|[1-9][0-9]{0,1})\n?$`)

// Run runs the command and returns its exit status.
func Run(argv []string, stdin io.Reader, stdout, stderr io.Writer) int {
	out, err := run(argv, stdin)
	if err != nil {
		switch e := err.(type) {
		case *exitError:
			fmt.Fprintf(stderr, "twig: %s\n", e.msg)
			if e.usage {
				io.WriteString(stderr, usage)
			}
			return e.status
		case *twig.Error:
			fmt.Fprintf(stderr, "%s: %s\n", e.ClassName(), e.Error())
			switch e.Kind {
			case twig.SyntaxErrorKind:
				return 2
			case twig.LoaderErrorKind:
				return 3
			}
			return 4
		case *php.Error:
			fmt.Fprintf(stderr, "%s: %s\n", e.Class, e.Msg)
			return 70
		case twig.FatalError:
			return 255
		}
		fmt.Fprintf(stderr, "Error: %s\n", err)
		return 70
	}
	io.WriteString(stdout, out)
	return 0
}

func str(s string) *string { return &s }

func parseArgs(argv []string) (*options, []string, error) {
	o := &options{autoescape: "html"}
	var positional []string
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		if arg == "--" {
			positional = append(positional, argv[i+1:]...)
			break
		}
		if arg == "-" || !strings.HasPrefix(arg, "-") {
			positional = append(positional, arg)
			continue
		}
		if arg == "--strict-variables" {
			o.strict = true
			continue
		}
		name, value, hasValue := arg, "", false
		if eq := strings.IndexByte(arg, '='); eq >= 0 {
			name, value, hasValue = arg[:eq], arg[eq+1:], true
		}
		key, ok := valued[name]
		if !ok {
			return nil, nil, usageError(`unknown option "%s"`, name)
		}
		if !hasValue {
			if i+1 >= len(argv) {
				return nil, nil, usageError(`option "%s" requires a value`, name)
			}
			i++
			value = argv[i]
		}
		switch key {
		case "namespaces", "globals":
			eq := strings.IndexByte(value, '=')
			if eq < 0 {
				return nil, nil, usageError(`option "%s" expects NAME=VALUE, got "%s"`, name, value)
			}
			p := pair{value[:eq], value[eq+1:]}
			if key == "namespaces" {
				o.namespaces = append(o.namespaces, p)
			} else {
				o.globals = append(o.globals, p)
			}
		case "autoescape":
			o.autoescape = value
		case "timezone":
			o.timezone = str(value)
		case "date_format":
			o.dateFormat = str(value)
		case "interval_format":
			o.intervalFmt = str(value)
		case "decimals":
			o.decimals = str(value)
		case "decimal_point":
			o.decimalPoint = str(value)
		case "thousands_sep":
			o.thousands = str(value)
		case "block":
			o.block = str(value)
		}
	}
	return o, positional, nil
}

// decodeJSON decodes JSON as json_decode($text, true) does, checking that a context is an object.
func decodeJSON(text, what string, requireObject bool) (any, error) {
	v, err := php.JSONDecode(text, false)
	if err != nil {
		return nil, fail(65, "invalid JSON in %s: %s", what, err.Error())
	}
	if requireObject {
		if _, ok := v.(*php.JSONObject); !ok {
			return nil, fail(65, "%s must be a JSON object", what)
		}
	}
	return php.JSONDecode(text, true)
}

func run(argv []string, stdin io.Reader) (string, error) {
	o, positional, err := parseArgs(argv)
	if err != nil {
		return "", err
	}
	if len(positional) < 2 || len(positional) > 3 {
		return "", usageError("expected 2 or 3 arguments, got %d", len(positional))
	}
	dir, name := positional[0], positional[1]
	known := false
	for _, s := range strategies {
		if s == o.autoescape {
			known = true
		}
	}
	if !known {
		return "", usageError(`invalid autoescape strategy "%s" (expected one of: %s)`, o.autoescape, strings.Join(strategies, ", "))
	}
	if o.decimals != nil && !decimalsRe.MatchString(*o.decimals) {
		return "", usageError(`invalid number of decimals "%s"`, *o.decimals)
	}
	if o.timezone != nil && !php.ValidTimezone(*o.timezone) {
		return "", usageError(`unknown timezone "%s"`, *o.timezone)
	}
	context := php.NewArray()
	if len(positional) == 3 {
		file := positional[2]
		var data []byte
		var rerr error
		if file == "-" {
			data, rerr = io.ReadAll(stdin)
		} else if st, err := os.Stat(file); err == nil && st.Mode().IsRegular() {
			data, rerr = os.ReadFile(file)
		} else {
			rerr = err
			if rerr == nil {
				rerr = fmt.Errorf("not a file")
			}
		}
		if rerr != nil {
			return "", fail(66, `cannot read context file "%s"`, file)
		}
		v, err := decodeJSON(string(data), "context", true)
		if err != nil {
			return "", err
		}
		context = v.(*php.Array)
	}
	cwd, _ := os.Getwd()
	loader := twig.NewFilesystemLoader(cwd)
	opts := twig.Options{Autoescape: o.autoescape, StrictVariables: o.strict, Timezone: "UTC",
		DateFormat: "F j, Y H:i", IntervalFormat: "%d days", Decimals: 0, DecimalPoint: ".", ThousandsSep: ","}
	if o.autoescape == "false" {
		opts.Autoescape = ""
	}
	if o.timezone != nil {
		opts.Timezone = *o.timezone
	}
	if o.dateFormat != nil || o.intervalFmt != nil {
		// CoreExtension::setDateFormat: a null argument keeps the current value
		if o.dateFormat != nil {
			opts.DateFormat = *o.dateFormat
		}
		if o.intervalFmt != nil {
			opts.IntervalFormat = *o.intervalFmt
		}
	}
	if o.decimals != nil {
		opts.Decimals = php.ToInt(*o.decimals)
	}
	if o.decimalPoint != nil {
		opts.DecimalPoint = *o.decimalPoint
	}
	if o.thousands != nil {
		opts.ThousandsSep = *o.thousands
	}
	if err := loader.AddPath(dir, "__main__"); err != nil {
		return "", err
	}
	for _, ns := range o.namespaces {
		if err := loader.AddPath(ns.value, ns.name); err != nil {
			return "", err
		}
	}
	env := twig.NewEnvironment(loader, opts)
	for _, g := range o.globals {
		v, err := decodeJSON(g.value, fmt.Sprintf(`global "%s"`, g.name), false)
		if err != nil {
			return "", err
		}
		env.AddGlobal(g.name, v)
	}
	if o.block != nil {
		return env.Render(name, context, *o.block, true)
	}
	return env.Render(name, context, "", false)
}
