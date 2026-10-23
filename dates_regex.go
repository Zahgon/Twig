// This file is part of the Go port of Twig.
//
// (c) Fabien Potencier
//
// For the full copyright and license information, please view the LICENSE
// file that was distributed with this source code.

package twig

import (
	"time"

	"github.com/twigphp/twig/internal/php"
)

// matches is CoreExtension::matches(regexp, str).
func (r *renderer) matches(re, subject any) any {
	pattern := strOf(re)
	s := ""
	if subject != nil {
		s = strOf(subject)
	}
	ok, err := php.PregMatch(pattern, s)
	switch e := err.(type) {
	case nil:
		// preg_match returns an int
		if ok {
			return int64(1)
		}
		return int64(0)
	case *php.PregWarning:
		panic(php.Runtime(`Regexp "%s" passed to "matches" is not valid: %s`, pattern, e.Msg))
	case *php.PregFailure:
		panic(php.Runtime(`Regexp "%s" passed to "matches" failed: %s.`, pattern, e.Msg))
	}
	panic(err)
}

// checkConstantRegexp is MatchesBinary's constructor: a constant pattern is compiled when the template is parsed.
func checkConstantRegexp(right Expr, line int) {
	c, ok := right.(*ConstExpr)
	if !ok {
		return
	}
	pattern := toStr(c.Value)
	if _, err := php.PregMatch(pattern, ""); err != nil {
		if w, ok := err.(*php.PregWarning); ok {
			panic(newSyntaxError(sprintf(`Regexp "%s" passed to "matches" is not valid: %s.`, pattern, w.Msg), line, nil))
		}
	}
}

// sortFilter is CoreExtension::sort: asort, or uasort with an arrow.
func (r *renderer) sortFilter(value, arrow any) any {
	a, ok := value.(*php.Array)
	if !ok {
		panic(php.Runtime(`The "sort" filter expects a sequence or a mapping, got "%s".`, php.TypeName(value)))
	}
	if arrow == nil {
		return php.Asort(a)
	}
	checkCallback("uasort", "", arrow)
	out, err := php.Uasort(a, func(x, y any) (any, error) { return r.callArrow(arrow, x, y), nil })
	throwErr(err)
	return out
}

func (r *renderer) defaultZone() *php.DateTimeZone {
	z, err := php.NewDateTimeZone(r.env.Timezone)
	throwErr(err)
	return z
}

// formatDate is CoreExtension::formatDate.
func (r *renderer) formatDate(value, format, tz any) any {
	f := r.env.DateFormat
	if format != nil {
		f = strOf(format)
	}
	d, err := php.ConvertDate(value, tz, r.defaultZone(), time.Now())
	throwErr(err)
	return php.FormatDate(d, f)
}

// modifyDate is CoreExtension::modifyDate.
func (r *renderer) modifyDate(value, modifier any) any {
	m := mustStr(php.StringArg("DateTime::modify", 1, "modifier", modifier))
	d, err := php.ModifyDate(value, m, r.defaultZone(), time.Now())
	throwErr(err)
	return d
}

// convertDate is the date() function.
func (r *renderer) convertDate(value, tz any) any {
	d, err := php.ConvertDate(value, tz, r.defaultZone(), time.Now())
	throwErr(err)
	return d
}
