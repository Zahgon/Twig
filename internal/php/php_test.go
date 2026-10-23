// This file is part of the Go port of Twig.
//
// (c) Fabien Potencier
//
// For the full copyright and license information, please view the LICENSE
// file that was distributed with this source code.

package php_test

import (
	"bufio"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/twigphp/twig/internal/php"
)

// TestPHPCases replays calls recorded with PHP 8.3.36 (testdata/php83_cases.tsv: a JSON call, a tab, PHP's result in
// the compact form of describe) and checks that the Go functions give the same values and exceptions.
func TestPHPCases(t *testing.T) {
	f, err := os.Open("testdata/php83_cases.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	failed := 0
	for n := 1; sc.Scan(); n++ {
		call, want, _ := strings.Cut(sc.Text(), "\t")
		got := run(call)
		if got != want {
			failed++
			if failed <= 20 {
				t.Errorf("line %d: %s\n  php: %s\n  go:  %s", n, call, want, got)
			}
		}
	}
	if failed > 0 {
		t.Errorf("%d cases differ from PHP", failed)
	}
}

func run(line string) string {
	v, err := php.JSONDecode(line, true)
	if err != nil {
		return "BADCASE"
	}
	args := v.(*php.Array).Values()
	for i, a := range args {
		if s, ok := a.(string); ok && strings.HasPrefix(s, "HEX:") {
			b, _ := hex.DecodeString(s[4:])
			args[i] = string(b)
		}
	}
	f, ok := fns[args[0].(string)]
	if !ok {
		return "NOFN"
	}
	res, err := f(args[1:]...)
	if err != nil {
		var pe *php.Error
		var re *php.RuntimeError
		switch {
		case errors.As(err, &pe):
			return "E:" + pe.Class + ":" + hex.EncodeToString([]byte(pe.Msg))
		case errors.As(err, &re):
			return "E:RuntimeError:" + hex.EncodeToString([]byte(re.Msg))
		}
		return "E:?:" + hex.EncodeToString([]byte(err.Error()))
	}
	return describe(res)
}

// describe renders a value with exact types: floats as their IEEE bits, strings in hexadecimal.
func describe(v any) string {
	switch x := v.(type) {
	case nil:
		return "N"
	case bool:
		if x {
			return "T"
		}
		return "F"
	case int64:
		return "i:" + strconv.FormatInt(x, 10)
	case float64:
		return fmt.Sprintf("d:%016x", math.Float64bits(x))
	case string:
		return "s:" + hex.EncodeToString([]byte(x))
	case php.Markup:
		return "o:Twig\\Markup"
	case *php.Array:
		o := "a{"
		x.Each(func(k, v any) bool {
			if i, ok := k.(int64); ok {
				o += "i:" + strconv.FormatInt(i, 10)
			} else {
				o += "s:" + hex.EncodeToString([]byte(k.(string)))
			}
			o += "=>" + describe(v) + ";"
			return true
		})
		return o + "}"
	}
	return fmt.Sprintf("o:%T", v)
}

type fn func(a ...any) (any, error)

func b2(f func(a, b any) (any, error)) fn {
	return func(a ...any) (any, error) { return f(a[0], a[1]) }
}
func bb(f func(a, b any) bool) fn { return func(a ...any) (any, error) { return f(a[0], a[1]), nil } }

var fns = map[string]fn{
	"add": b2(php.Add), "sub": b2(php.Sub), "mul": b2(php.Mul), "div": b2(php.Div), "mod": b2(php.Mod), "pow": b2(php.Pow),
	"floordiv": b2(php.FloorDiv), "neg": func(a ...any) (any, error) { return php.Neg(a[0]) },
	"concat": func(a ...any) (any, error) { return php.Concat(a[0], a[1]) },
	"band":   b2(php.BitAnd), "bor": b2(php.BitOr), "bxor": b2(php.BitXor),
	"cmp": func(a ...any) (any, error) { return php.Spaceship(a[0], a[1]), nil },
	"eq":  bb(php.Equal), "lt": bb(php.Less), "gt": bb(php.Greater), "le": bb(php.LessEq), "ge": bb(php.GreaterEq), "ident": bb(php.Identical),
	"tostr":   func(a ...any) (any, error) { return php.ToString(a[0]) },
	"toint":   func(a ...any) (any, error) { return php.ToInt(a[0]), nil },
	"tofloat": func(a ...any) (any, error) { return php.ToFloat(a[0]), nil },
	"tobool":  func(a ...any) (any, error) { return php.ToBool(a[0]), nil },
	"upper": func(a ...any) (any, error) {
		s, err := php.StringArg("mb_strtoupper", 1, "string", a[0])
		return php.MbStrtoupper(s), err
	},
	"lower": func(a ...any) (any, error) {
		s, err := php.StringArg("mb_strtolower", 1, "string", a[0])
		return php.MbStrtolower(s), err
	},
	"title": func(a ...any) (any, error) {
		s, err := php.StringArg("mb_convert_case", 1, "string", a[0])
		return php.MbTitle(s), err
	},
	"strlen": func(a ...any) (any, error) {
		s, err := php.StringArg("mb_strlen", 1, "string", a[0])
		return php.MbStrlen(s), err
	},
	"substr": func(a ...any) (any, error) {
		s, err := php.StringArg("mb_substr", 1, "string", a[0])
		if err != nil {
			return nil, err
		}
		st, err := php.IntArg("mb_substr", 2, "start", a[1])
		if err != nil {
			return nil, err
		}
		var l any
		if len(a) > 2 {
			l = a[2]
		}
		n, has, err := php.NullableIntArg("mb_substr", 3, "length", l)
		if err != nil {
			return nil, err
		}
		return php.MbSubstr(s, st, n, has), nil
	},
	"trim":  func(a ...any) (any, error) { return php.TrimChars(a[0].(string), a[1].(string), true, true), nil },
	"nl2br": func(a ...any) (any, error) { return php.Nl2br(a[0].(string)), nil },
	"striptags": func(a ...any) (any, error) {
		var t any
		if len(a) > 1 {
			t = a[1]
		}
		al, err := php.AllowedTags(t)
		return php.StripTags(a[0].(string), al), err
	},
	"explode": func(a ...any) (any, error) {
		if len(a) > 2 {
			return php.Explode(a[0].(string), a[1].(string), php.ToInt(a[2]), true)
		}
		return php.Explode(a[0].(string), a[1].(string), 0, false)
	},
	"implode": func(a ...any) (any, error) { return php.Implode(a[0].(string), a[1].(*php.Array)) },
	"strtr":   func(a ...any) (any, error) { return php.Strtr(a[0].(string), a[1].(*php.Array)) },
	"rawurl":  func(a ...any) (any, error) { return php.RawURLEncode(a[0].(string)), nil },
	"httpq":   func(a ...any) (any, error) { return php.HTTPBuildQuery(a[0].(*php.Array)), nil },
	"jsonenc": func(a ...any) (any, error) {
		f := int64(0)
		if len(a) > 1 {
			f = php.ToInt(a[1])
		}
		s, ok, _ := php.JSONEncode(a[0], f, 0)
		if !ok {
			return false, nil
		}
		return s, nil
	},
	"jsondec": func(a ...any) (any, error) {
		v, err := php.JSONDecode(a[0].(string), true)
		if err != nil {
			return "ERR:" + err.Error(), nil
		}
		return v, nil
	},
	"jsondecobj": func(a ...any) (any, error) {
		_, err := php.JSONDecode(a[0].(string), false)
		if err != nil {
			return "ERR:" + err.Error(), nil
		}
		return "ok", nil
	},
	"round": func(a ...any) (any, error) {
		p := int64(0)
		if len(a) > 1 {
			p = php.ToInt(a[1])
		}
		return php.PHPRound(php.ToFloat(a[0]), p, php.RoundHalfUp), nil
	},
	"nf": func(a ...any) (any, error) {
		d, pt, sp := int64(0), ".", ","
		if len(a) > 1 {
			d = php.ToInt(a[1])
		}
		if len(a) > 2 {
			pt = a[2].(string)
		}
		if len(a) > 3 {
			sp = a[3].(string)
		}
		return php.NumberFormat(php.ToFloat(a[0]), d, pt, sp), nil
	},
	"sprintf": func(a ...any) (any, error) { return php.Sprintf(a[0].(string), a[1:]...) },
	"tround":  func(a ...any) (any, error) { return php.Round(a[0], a[1], a[2].(string)) },
	"range": func(a ...any) (any, error) {
		var s any
		if len(a) > 2 {
			s = a[2]
		}
		return php.Range(a[0], a[1], s)
	},
	"max":   func(a ...any) (any, error) { return php.Max(a) },
	"min":   func(a ...any) (any, error) { return php.Min(a) },
	"abs":   func(a ...any) (any, error) { return php.Abs(a[0]) },
	"asort": func(a ...any) (any, error) { return php.Asort(a[0].(*php.Array)), nil },
	"uas_half": func(a ...any) (any, error) {
		return php.Uasort(a[0].(*php.Array), func(x, y any) (any, error) { return float64(php.Spaceship(x, y)) * 0.5, nil })
	},
	"uas_bool": func(a ...any) (any, error) {
		return php.Uasort(a[0].(*php.Array), func(x, y any) (any, error) { return php.Greater(x, y), nil })
	},
	"uas_rev": func(a ...any) (any, error) {
		return php.Uasort(a[0].(*php.Array), func(x, y any) (any, error) { return php.Spaceship(y, x), nil })
	},
	"merge": func(a ...any) (any, error) {
		var arrs []*php.Array
		for _, x := range a {
			arrs = append(arrs, x.(*php.Array))
		}
		return php.ArrayMerge(arrs...), nil
	},
	"slice": func(a ...any) (any, error) {
		var l int64
		has := len(a) > 2 && a[2] != nil
		if has {
			l = php.ToInt(a[2])
		}
		p := len(a) > 3 && php.ToBool(a[3])
		return php.ArraySlice(a[0].(*php.Array), php.ToInt(a[1]), l, has, p), nil
	},
	"rev": func(a ...any) (any, error) {
		return php.ArrayReverse(a[0].(*php.Array), len(a) > 1 && php.ToBool(a[1])), nil
	},
	"chunk": func(a ...any) (any, error) {
		return php.ArrayChunk(a[0].(*php.Array), php.ToInt(a[1]), len(a) > 2 && php.ToBool(a[2]))
	},
	"pm": func(a ...any) (any, error) {
		ok, err := php.PregMatch(a[0].(string), a[1].(string))
		if err != nil {
			if w, isW := err.(*php.PregWarning); isW {
				return "W:: " + w.Msg, nil
			}
			return "F:" + err.Error(), nil
		}
		if ok {
			return int64(1), nil
		}
		return int64(0), nil
	},
	"date": func(a ...any) (any, error) {
		def := "UTC"
		if len(a) > 3 {
			def = a[3].(string)
		}
		dz, err := php.NewDateTimeZone(def)
		if err != nil {
			return nil, err
		}
		var tz any
		if len(a) > 2 {
			tz = a[2]
		}
		d, err := php.ConvertDate(a[0], tz, dz, time.Now())
		if err != nil {
			return nil, err
		}
		return d.Format(a[1].(string)), nil
	},
	"dmod": func(a ...any) (any, error) {
		dz, _ := php.NewDateTimeZone("UTC")
		d, err := php.ConvertDate(a[0], false, dz, time.Now())
		if err != nil {
			return nil, err
		}
		if err := d.Modify(a[1].(string)); err != nil {
			return nil, err
		}
		var tz any
		if len(a) > 3 {
			tz = a[3]
		}
		d, err = php.ConvertDate(d, tz, dz, time.Now())
		if err != nil {
			return nil, err
		}
		return d.Format(a[2].(string)), nil
	},
	"esc": func(a ...any) (any, error) {
		v, err := php.Escape(a[0], a[1].(string), false)
		if m, ok := v.(php.Markup); ok {
			return m.S, err
		}
		return v, err
	},
	"fstr":  func(a ...any) (any, error) { return php.FormatFloat(php.ToFloat(a[0])), nil },
	"fjson": func(a ...any) (any, error) { s, _, _ := php.JSONEncode(php.ToFloat(a[0]), 0, 0); return s, nil },
}
