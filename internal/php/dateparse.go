// This file is part of the Go port of Twig.
//
// (c) Fabien Potencier
//
// For the full copyright and license information, please view the LICENSE
// file that was distributed with this source code.

package php

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

// PHP's date/time string parser (timelib's parse_date.re, by Derick Rethans, MIT licensed), as used by
// new DateTime() and DateTime::modify(). The scanner tries every rule at the current position and takes the
// longest match, the earliest rule winning ties, exactly like the re2c-generated original.

const tlUnset = -9999999

const (
	zoneTypeOffset = 1
	zoneTypeAbbr   = 2
	zoneTypeID     = 3
)

const (
	specialWeekday              = 1
	specialDayOfWeekInMonth     = 2
	specialLastDayOfWeekInMonth = 3
	specialFirstDayOfMonth      = 1
	specialLastDayOfMonth       = 2
)

type tzAbbr struct {
	dst    int
	offset int64
}

type relTime struct {
	y, m, d, h, i, s, us int64
	weekday              int64
	weekdayBehavior      int
	firstLastDayOf       int
	specialType          int
	specialAmount        int64
	haveWeekdayRelative  bool
	haveSpecialRelative  bool
}

// tlTime is timelib_time.
type tlTime struct {
	y, m, d, h, i, s, us int64
	z                    int64
	dst                  int64
	zoneType             int
	tzAbbr               string
	tz                   *TimeZoneInfo
	sse                  int64
	isLocaltime          bool
	haveTime, haveDate   int
	haveZone             int
	haveRelative         bool
	rel                  relTime
}

type tlError struct {
	pos  int
	char byte
	msg  string
}

type dateScanner struct {
	str      []byte // input plus NUL padding
	n        int    // length of the real input
	tok      int
	t        *tlTime
	errors   []tlError
	warnings []tlError
}

func (s *dateScanner) addError(msg string) {
	s.errors = append(s.errors, tlError{s.tok, s.str[s.tok], msg})
}

func (s *dateScanner) addWarning(msg string) {
	s.warnings = append(s.warnings, tlError{s.tok, s.str[s.tok], msg})
}

// Regular expression fragments of parse_date.re, filled once by init and only read afterwards.
var dateFragments = map[string]string{}

func init() {
	f := func(name, re string) {
		for k, v := range dateFragments {
			re = strings.ReplaceAll(re, "{"+k+"}", v)
		}
		dateFragments[name] = "(?:" + re + ")"
	}
	f("space", `[ \t]+|\x{a0}+|\x{202f}+`)
	f("frac", `\.[0-9]+`)
	f("hour24", `[01]?[0-9]|2[0-4]`)
	f("hour24lz", `[01][0-9]|2[0-4]`)
	f("hour12", `0?[1-9]|1[0-2]`)
	f("minute", `[0-5]?[0-9]`)
	f("minutelz", `[0-5][0-9]`)
	f("second", `[0-5]?[0-9]|60`)
	f("secondlz", `[0-5][0-9]|60`)
	f("meridian", `[AaPp]\.?[Mm]\.?[\x00\t ]`)
	f("tz", `\(?[A-Za-z]{1,6}\)?|[A-Z][a-z]+(?:[_/-][A-Za-z]+)+`)
	f("tzcorrection", `(?:GMT)?[+-](?:{hour24}(?::?{minute})?|{hour24lz}{minutelz}{secondlz}|{hour24lz}:{minutelz}:{secondlz})`)
	f("daysuf", `st|nd|rd|th`)
	f("month", `0?[0-9]|1[0-2]`)
	f("day", `(?:[0-2]?[0-9]|3[01]){daysuf}?`)
	f("year", `[0-9]{1,4}`)
	f("year2", `[0-9]{2}`)
	f("year4", `[0-9]{4}`)
	f("year4withsign", `[+-]?[0-9]{4}`)
	f("yearx", `[+-][0-9]{5,19}`)
	f("dayofyear", `00[1-9]|0[1-9][0-9]|[1-2][0-9][0-9]|3[0-5][0-9]|36[0-6]`)
	f("weekofyear", `0[1-9]|[1-4][0-9]|5[0-3]`)
	f("monthlz", `0[0-9]|1[0-2]`)
	f("daylz", `0[0-9]|[1-2][0-9]|3[01]`)
	f("dayfulls", `(?i:sundays|mondays|tuesdays|wednesdays|thursdays|fridays|saturdays)`)
	f("dayfull", `(?i:sunday|monday|tuesday|wednesday|thursday|friday|saturday)`)
	f("dayabbr", `(?i:sun|mon|tue|wed|thu|fri|sat)`)
	f("dayspecial", `(?i:weekday|weekdays)`)
	f("daytext", `{dayfulls}|{dayfull}|{dayabbr}|{dayspecial}`)
	f("monthfull", `(?i:january|february|march|april|may|june|july|august|september|october|november|december)`)
	f("monthabbr", `(?i:jan|feb|mar|apr|may|jun|jul|aug|sep|sept|oct|nov|dec)`)
	f("monthroman", `I|II|III|IV|V|VI|VII|VIII|IX|X|XI|XII`)
	f("monthtext", `{monthfull}|{monthabbr}|{monthroman}`)
	f("timetiny12", `{hour12}{space}?{meridian}`)
	f("timeshort12", `{hour12}[:.]{minutelz}{space}?{meridian}`)
	f("timelong12", `{hour12}[:.]{minute}[:.]{secondlz}{space}?{meridian}`)
	f("timetiny24", `[tT]{hour24}`)
	f("timeshort24", `[tT]?{hour24}[:.]{minute}`)
	f("timelong24", `[tT]?{hour24}[:.]{minute}[:.]{second}`)
	f("iso8601long", `[tT]?{hour24}[:.]{minute}[:.]{second}{frac}`)
	f("iso8601normtz", `[tT]?{hour24}[:.]{minute}[:.]{secondlz}{space}?(?:{tzcorrection}|{tz})`)
	f("gnunocolon", `[tT]?{hour24lz}{minutelz}`)
	f("iso8601nocolon", `[tT]?{hour24lz}{minutelz}{secondlz}`)
	f("americanshort", `{month}/{day}`)
	f("american", `{month}/{day}/{year}`)
	f("iso8601dateslash", `{year4}/{monthlz}/{daylz}/?`)
	f("dateslash", `{year4}/{month}/{day}`)
	f("iso8601date4", `{year4withsign}-{monthlz}-{daylz}`)
	f("iso8601date2", `{year2}-{monthlz}-{daylz}`)
	f("iso8601datex", `{yearx}-{monthlz}-{daylz}`)
	f("gnudateshorter", `{year4}-{month}`)
	f("gnudateshort", `{year}-{month}-{day}`)
	f("pointeddate4", `{day}[.\t-]{month}[.-]{year4}`)
	f("pointeddate2", `{day}[.\t]{month}\.{year2}`)
	f("datefull", `{day}[ \t.-]*{monthtext}[ \t.-]*{year}`)
	f("datenoday", `{monthtext}[ .\t-]*{year4}`)
	f("datenodayrev", `{year4}[ .\t-]*{monthtext}`)
	f("datetextual", `{monthtext}[ .\t-]*{day}[,.stndrh\t ]+{year}`)
	f("datenoyear", `{monthtext}[ .\t-]*{day}(?:[,.stndrh\t ]+|\x00)`)
	f("datenoyearrev", `{day}[ .\t-]*{monthtext}`)
	f("datenocolon", `{year4}{monthlz}{daylz}`)
	f("soap", `{year4}-{monthlz}-{daylz}T{hour24lz}:{minutelz}:{secondlz}{frac}{tzcorrection}?`)
	f("xmlrpc", `{year4}{monthlz}{daylz}T{hour24}:{minutelz}:{secondlz}`)
	f("xmlrpcnocolon", `{year4}{monthlz}{daylz}[tT]{hour24}{minutelz}{secondlz}`)
	f("wddx", `{year4}-{month}-{day}T{hour24}:{minute}:{second}`)
	f("pgydotd", `{year4}[.-]?{dayofyear}`)
	f("pgtextshort", `{monthabbr}-{daylz}-{year}`)
	f("pgtextreverse", `{year}-{monthabbr}-{daylz}`)
	f("mssqltime", `{hour12}:{minutelz}:{secondlz}[:.][0-9]+{meridian}`)
	f("isoweekday", `{year4}-?W{weekofyear}-?[0-7]`)
	f("isoweek", `{year4}-?W{weekofyear}`)
	f("exif", `{year4}:{monthlz}:{daylz} {hour24lz}:{minutelz}:{secondlz}`)
	f("firstdayof", `(?i:first day of)`)
	f("lastdayof", `(?i:last day of)`)
	f("backof", `(?i:back of ){hour24}(?:{space}?{meridian})?`)
	f("frontof", `(?i:front of ){hour24}(?:{space}?{meridian})?`)
	f("clf", `{day}/{monthabbr}/{year4}:{hour24lz}:{minutelz}:{secondlz}{space}{tzcorrection}`)
	f("timestamp", `@-?[0-9]+`)
	f("timestampms", `@-?[0-9]+\.[0-9]{0,6}`)
	f("dateshortwithtimeshort12", `{datenoyear}{timeshort12}`)
	f("dateshortwithtimelong12", `{datenoyear}{timelong12}`)
	f("dateshortwithtimeshort", `{datenoyear}{timeshort24}`)
	f("dateshortwithtimelong", `{datenoyear}{timelong24}`)
	f("dateshortwithtimelongtz", `{datenoyear}{iso8601normtz}`)
	f("reltextnumber", `(?i:first|second|third|fourth|fifth|sixth|seventh|eight|eighth|ninth|tenth|eleventh|twelfth)`)
	f("reltexttext", `(?i:next|last|previous|this)`)
	f("reltextunit", `(?i:ms|µs)|(?i:(?:msec|millisecond|µsec|microsecond|usec|sec|second|min|minute|hour|day|fortnight|forthnight|month|year)s?)|(?i:weeks)|{daytext}`)
	f("relnumber", `[+-]*[ \t]*[0-9]{1,13}`)
	f("relative", `{relnumber}{space}?(?:{reltextunit}|(?i:week))`)
	f("relativetext", `(?:{reltextnumber}|{reltexttext}){space}{reltextunit}`)
	f("relativetextweek", `{reltexttext}{space}(?i:week)`)
	f("weekdayof", `(?:{reltextnumber}|{reltexttext}){space}(?:{dayfulls}|{dayfull}|{dayabbr}){space}(?i:of)`)
	for _, r := range dateRuleDefs {
		parts := make([]string, len(r.names))
		for i, n := range r.names {
			if strings.HasPrefix(n, "=") {
				parts[i] = n[1:]
			} else {
				parts[i] = dateFragments[n]
			}
		}
		re := regexp.MustCompile(`^(?:` + strings.Join(parts, "|") + `)`)
		re.Longest()
		dateRules = append(dateRules, dateRule{re, r.action})
	}
}

type dateRule struct {
	re     *regexp.Regexp
	action func(s *dateScanner, tok []byte) bool
}

var dateRules []dateRule

// ruleResult: an action returns false when it reported an error (TIMELIB_ERROR).
var dateRuleDefs = []struct {
	names  []string
	action func(s *dateScanner, tok []byte) bool
}{
	{[]string{"=(?i:yesterday)"}, func(s *dateScanner, tok []byte) bool {
		s.t.haveRelative = true
		s.unhaveTime()
		s.t.rel.d = -1
		return true
	}},
	{[]string{"=(?i:now)"}, func(s *dateScanner, tok []byte) bool { return true }},
	{[]string{"=(?i:noon)"}, func(s *dateScanner, tok []byte) bool {
		s.unhaveTime()
		if !s.haveTime() {
			return false
		}
		s.t.h = 12
		return true
	}},
	{[]string{"=(?i:midnight|today)"}, func(s *dateScanner, tok []byte) bool {
		s.unhaveTime()
		return true
	}},
	{[]string{"=(?i:tomorrow)"}, func(s *dateScanner, tok []byte) bool {
		s.t.haveRelative = true
		s.unhaveTime()
		s.t.rel.d = 1
		return true
	}},
	{[]string{"timestamp"}, func(s *dateScanner, tok []byte) bool {
		s.t.haveRelative = true
		s.unhaveDate()
		s.unhaveTime()
		if !s.haveTZ() {
			return false
		}
		p := 0
		i := s.getSignedNr(tok, &p, 24)
		s.setEpoch()
		s.t.rel.s += i
		return true
	}},
	{[]string{"timestampms"}, func(s *dateScanner, tok []byte) bool {
		s.t.haveRelative = true
		s.unhaveDate()
		s.unhaveTime()
		if !s.haveTZ() {
			return false
		}
		neg := tok[1] == '-'
		p := 0
		i := s.getSignedNr(tok, &p, 24)
		before := p
		us := s.getSignedNr(tok, &p, 6)
		us = int64(float64(us) * math.Pow(10, float64(7-(p-before))))
		if neg {
			us = -us
		}
		s.setEpoch()
		s.t.rel.s += i
		s.t.rel.us = us
		return true
	}},
	{[]string{"firstdayof", "lastdayof"}, func(s *dateScanner, tok []byte) bool {
		s.t.haveRelative = true
		if tok[0] == 'l' || tok[0] == 'L' {
			s.t.rel.firstLastDayOf = specialLastDayOfMonth
		} else {
			s.t.rel.firstLastDayOf = specialFirstDayOfMonth
		}
		return true
	}},
	{[]string{"backof", "frontof"}, func(s *dateScanner, tok []byte) bool {
		s.unhaveTime()
		if !s.haveTime() {
			return false
		}
		p := 0
		if tok[0] == 'b' {
			s.t.h = getNr(tok, &p, 2)
			s.t.i = 15
		} else {
			s.t.h = getNr(tok, &p, 2) - 1
			s.t.i = 45
		}
		if tok[p] != 0 {
			eatSpaces(tok, &p)
			s.t.h += meridian(tok, &p, s.t.h)
		}
		return true
	}},
	{[]string{"weekdayof"}, func(s *dateScanner, tok []byte) bool {
		s.t.haveRelative = true
		s.t.rel.haveSpecialRelative = true
		p := 0
		behavior := 0
		i := getRelativeText(tok, &p, &behavior)
		eatSpaces(tok, &p)
		if i > 0 {
			s.t.rel.specialType = specialDayOfWeekInMonth
			s.setRelative(tok, &p, i, 1, false)
		} else {
			s.t.rel.specialType = specialLastDayOfWeekInMonth
			s.setRelative(tok, &p, i, behavior, false)
		}
		return true
	}},
	{[]string{"timetiny12", "timeshort12", "timelong12"}, func(s *dateScanner, tok []byte) bool {
		if !s.haveTime() {
			return false
		}
		p := 0
		s.t.h = getNr(tok, &p, 2)
		if tok[p] == ':' || tok[p] == '.' {
			s.t.i = getNr(tok, &p, 2)
			if tok[p] == ':' || tok[p] == '.' {
				s.t.s = getNr(tok, &p, 2)
			}
		}
		eatSpaces(tok, &p)
		s.t.h += meridian(tok, &p, s.t.h)
		return true
	}},
	{[]string{"mssqltime"}, func(s *dateScanner, tok []byte) bool {
		if !s.haveTime() {
			return false
		}
		p := 0
		s.t.h = getNr(tok, &p, 2)
		s.t.i = getNr(tok, &p, 2)
		if tok[p] == ':' || tok[p] == '.' {
			s.t.s = getNr(tok, &p, 2)
			if tok[p] == ':' || tok[p] == '.' {
				s.t.us = getFracNr(tok, &p)
			}
		}
		eatSpaces(tok, &p)
		s.t.h += meridian(tok, &p, s.t.h)
		return true
	}},
	{[]string{"timetiny24", "timeshort24", "timelong24", "iso8601long"}, func(s *dateScanner, tok []byte) bool {
		if !s.haveTime() {
			return false
		}
		p := 0
		s.t.h = getNr(tok, &p, 2)
		if tok[p] == ':' || tok[p] == '.' {
			s.t.i = getNr(tok, &p, 2)
			if tok[p] == ':' || tok[p] == '.' {
				s.t.s = getNr(tok, &p, 2)
				if tok[p] == '.' {
					s.t.us = getFracNr(tok, &p)
				}
			}
		}
		if tok[p] != 0 {
			s.parseZoneInto(tok, &p)
		}
		return true
	}},
	{[]string{"gnunocolon"}, func(s *dateScanner, tok []byte) bool {
		p := 0
		switch s.t.haveTime {
		case 0:
			s.t.h = getNr(tok, &p, 2)
			s.t.i = getNr(tok, &p, 2)
			s.t.s = 0
		case 1:
			s.t.y = getNr(tok, &p, 4)
		default:
			s.addError("Double time specification")
			return false
		}
		s.t.haveTime++
		return true
	}},
	{[]string{"iso8601nocolon"}, func(s *dateScanner, tok []byte) bool {
		if !s.haveTime() {
			return false
		}
		p := 0
		s.t.h = getNr(tok, &p, 2)
		s.t.i = getNr(tok, &p, 2)
		s.t.s = getNr(tok, &p, 2)
		if tok[p] != 0 {
			s.parseZoneInto(tok, &p)
		}
		return true
	}},
	{[]string{"americanshort", "american"}, func(s *dateScanner, tok []byte) bool {
		if !s.haveDate() {
			return false
		}
		p := 0
		s.t.m = getNr(tok, &p, 2)
		s.t.d = getNr(tok, &p, 2)
		if tok[p] == '/' {
			length := 0
			s.t.y = getNrEx(tok, &p, 4, &length)
			s.t.y = processYear(s.t.y, length)
		}
		return true
	}},
	{[]string{"iso8601date4", "iso8601dateslash", "dateslash"}, func(s *dateScanner, tok []byte) bool {
		if !s.haveDate() {
			return false
		}
		p := 0
		s.t.y = s.getSignedNr(tok, &p, 4)
		s.t.m = getNr(tok, &p, 2)
		s.t.d = getNr(tok, &p, 2)
		return true
	}},
	{[]string{"iso8601date2"}, func(s *dateScanner, tok []byte) bool {
		if !s.haveDate() {
			return false
		}
		p, length := 0, 0
		s.t.y = getNrEx(tok, &p, 4, &length)
		s.t.m = getNr(tok, &p, 2)
		s.t.d = getNr(tok, &p, 2)
		s.t.y = processYear(s.t.y, length)
		return true
	}},
	{[]string{"iso8601datex"}, func(s *dateScanner, tok []byte) bool {
		if !s.haveDate() {
			return false
		}
		p := 0
		s.t.y = s.getSignedNr(tok, &p, 19)
		s.t.m = getNr(tok, &p, 2)
		s.t.d = getNr(tok, &p, 2)
		return true
	}},
	{[]string{"gnudateshorter"}, func(s *dateScanner, tok []byte) bool {
		if !s.haveDate() {
			return false
		}
		p, length := 0, 0
		s.t.y = getNrEx(tok, &p, 4, &length)
		s.t.m = getNr(tok, &p, 2)
		s.t.d = 1
		s.t.y = processYear(s.t.y, length)
		return true
	}},
	{[]string{"gnudateshort"}, func(s *dateScanner, tok []byte) bool {
		if !s.haveDate() {
			return false
		}
		p, length := 0, 0
		s.t.y = getNrEx(tok, &p, 4, &length)
		s.t.m = getNr(tok, &p, 2)
		s.t.d = getNr(tok, &p, 2)
		s.t.y = processYear(s.t.y, length)
		return true
	}},
	{[]string{"datefull"}, func(s *dateScanner, tok []byte) bool {
		if !s.haveDate() {
			return false
		}
		p, length := 0, 0
		s.t.d = getNr(tok, &p, 2)
		skipDaySuffix(tok, &p)
		s.t.m = getMonth(tok, &p)
		s.t.y = getNrEx(tok, &p, 4, &length)
		s.t.y = processYear(s.t.y, length)
		return true
	}},
	{[]string{"pointeddate4"}, func(s *dateScanner, tok []byte) bool {
		if !s.haveDate() {
			return false
		}
		p := 0
		s.t.d = getNr(tok, &p, 2)
		s.t.m = getNr(tok, &p, 2)
		s.t.y = getNr(tok, &p, 4)
		return true
	}},
	{[]string{"pointeddate2"}, func(s *dateScanner, tok []byte) bool {
		if !s.haveDate() {
			return false
		}
		p, length := 0, 0
		s.t.d = getNr(tok, &p, 2)
		s.t.m = getNr(tok, &p, 2)
		s.t.y = getNrEx(tok, &p, 2, &length)
		s.t.y = processYear(s.t.y, length)
		return true
	}},
	{[]string{"datenoday"}, func(s *dateScanner, tok []byte) bool {
		if !s.haveDate() {
			return false
		}
		p, length := 0, 0
		s.t.m = getMonth(tok, &p)
		s.t.y = getNrEx(tok, &p, 4, &length)
		s.t.d = 1
		s.t.y = processYear(s.t.y, length)
		return true
	}},
	{[]string{"datenodayrev"}, func(s *dateScanner, tok []byte) bool {
		if !s.haveDate() {
			return false
		}
		p, length := 0, 0
		s.t.y = getNrEx(tok, &p, 4, &length)
		s.t.m = getMonth(tok, &p)
		s.t.d = 1
		s.t.y = processYear(s.t.y, length)
		return true
	}},
	{[]string{"datetextual", "datenoyear"}, func(s *dateScanner, tok []byte) bool {
		if !s.haveDate() {
			return false
		}
		p, length := 0, 0
		s.t.m = getMonth(tok, &p)
		s.t.d = getNr(tok, &p, 2)
		s.t.y = getNrEx(tok, &p, 4, &length)
		s.t.y = processYear(s.t.y, length)
		return true
	}},
	{[]string{"datenoyearrev"}, func(s *dateScanner, tok []byte) bool {
		if !s.haveDate() {
			return false
		}
		p := 0
		s.t.d = getNr(tok, &p, 2)
		skipDaySuffix(tok, &p)
		s.t.m = getMonth(tok, &p)
		return true
	}},
	{[]string{"datenocolon"}, func(s *dateScanner, tok []byte) bool {
		if !s.haveDate() {
			return false
		}
		p := 0
		s.t.y = getNr(tok, &p, 4)
		s.t.m = getNr(tok, &p, 2)
		s.t.d = getNr(tok, &p, 2)
		return true
	}},
	{[]string{"xmlrpc", "xmlrpcnocolon", "soap", "wddx", "exif"}, func(s *dateScanner, tok []byte) bool {
		if !s.haveTime() || !s.haveDate() {
			return false
		}
		p := 0
		s.t.y = getNr(tok, &p, 4)
		s.t.m = getNr(tok, &p, 2)
		s.t.d = getNr(tok, &p, 2)
		s.t.h = getNr(tok, &p, 2)
		s.t.i = getNr(tok, &p, 2)
		s.t.s = getNr(tok, &p, 2)
		if tok[p] == '.' {
			s.t.us = getFracNr(tok, &p)
			if tok[p] != 0 {
				s.parseZoneInto(tok, &p)
			}
		}
		return true
	}},
	{[]string{"pgydotd"}, func(s *dateScanner, tok []byte) bool {
		if !s.haveDate() {
			return false
		}
		p, length := 0, 0
		s.t.y = getNrEx(tok, &p, 4, &length)
		s.t.d = getNr(tok, &p, 3)
		s.t.m = 1
		s.t.y = processYear(s.t.y, length)
		return true
	}},
	{[]string{"isoweekday"}, func(s *dateScanner, tok []byte) bool {
		if !s.haveDate() {
			return false
		}
		s.t.haveRelative = true
		p := 0
		s.t.y = getNr(tok, &p, 4)
		w := getNr(tok, &p, 2)
		d := getNr(tok, &p, 1)
		s.t.m, s.t.d = 1, 1
		s.t.rel.d = daynrFromWeeknr(s.t.y, w, d)
		return true
	}},
	{[]string{"isoweek"}, func(s *dateScanner, tok []byte) bool {
		if !s.haveDate() {
			return false
		}
		s.t.haveRelative = true
		p := 0
		s.t.y = getNr(tok, &p, 4)
		w := getNr(tok, &p, 2)
		s.t.m, s.t.d = 1, 1
		s.t.rel.d = daynrFromWeeknr(s.t.y, w, 1)
		return true
	}},
	{[]string{"pgtextshort"}, func(s *dateScanner, tok []byte) bool {
		if !s.haveDate() {
			return false
		}
		p, length := 0, 0
		s.t.m = getMonth(tok, &p)
		s.t.d = getNr(tok, &p, 2)
		s.t.y = getNrEx(tok, &p, 4, &length)
		s.t.y = processYear(s.t.y, length)
		return true
	}},
	{[]string{"pgtextreverse"}, func(s *dateScanner, tok []byte) bool {
		if !s.haveDate() {
			return false
		}
		p, length := 0, 0
		s.t.y = getNrEx(tok, &p, 4, &length)
		s.t.m = getMonth(tok, &p)
		s.t.d = getNr(tok, &p, 2)
		s.t.y = processYear(s.t.y, length)
		return true
	}},
	{[]string{"clf"}, func(s *dateScanner, tok []byte) bool {
		if !s.haveTime() || !s.haveDate() {
			return false
		}
		p := 0
		s.t.d = getNr(tok, &p, 2)
		s.t.m = getMonth(tok, &p)
		s.t.y = getNr(tok, &p, 4)
		s.t.h = getNr(tok, &p, 2)
		s.t.i = getNr(tok, &p, 2)
		s.t.s = getNr(tok, &p, 2)
		eatSpaces(tok, &p)
		s.parseZoneInto(tok, &p)
		return true
	}},
	{[]string{"year4"}, func(s *dateScanner, tok []byte) bool {
		p := 0
		s.t.y = getNr(tok, &p, 4)
		return true
	}},
	{[]string{"=(?i:ago)"}, func(s *dateScanner, tok []byte) bool {
		r := &s.t.rel
		r.y, r.m, r.d, r.h, r.i, r.s = -r.y, -r.m, -r.d, -r.h, -r.i, -r.s
		r.weekday = -r.weekday
		if r.weekday == 0 {
			r.weekday = -7
		}
		if r.haveSpecialRelative && r.specialType == specialWeekday {
			r.specialAmount = -r.specialAmount
		}
		return true
	}},
	{[]string{"daytext"}, func(s *dateScanner, tok []byte) bool {
		s.t.haveRelative = true
		s.t.rel.haveWeekdayRelative = true
		s.unhaveTime()
		p := 0
		unit := lookupRelunit(tok, &p)
		if unit != nil {
			s.t.rel.weekday = unit.multiplier
		}
		if s.t.rel.weekdayBehavior != 2 {
			s.t.rel.weekdayBehavior = 1
		}
		return true
	}},
	{[]string{"relativetextweek"}, func(s *dateScanner, tok []byte) bool {
		s.t.haveRelative = true
		p := 0
		for tok[p] != 0 {
			behavior := 0
			i := getRelativeText(tok, &p, &behavior)
			eatSpaces(tok, &p)
			s.setRelative(tok, &p, i, behavior, false)
			s.t.rel.weekdayBehavior = 2
			if !s.t.rel.haveWeekdayRelative {
				s.t.haveRelative = true
				s.t.rel.haveWeekdayRelative = true
				s.t.rel.weekday = 1
			}
		}
		return true
	}},
	{[]string{"relativetext"}, func(s *dateScanner, tok []byte) bool {
		s.t.haveRelative = true
		p := 0
		for tok[p] != 0 {
			behavior := 0
			i := getRelativeText(tok, &p, &behavior)
			eatSpaces(tok, &p)
			s.setRelative(tok, &p, i, behavior, false)
		}
		return true
	}},
	{[]string{"monthfull", "monthabbr"}, func(s *dateScanner, tok []byte) bool {
		if !s.haveDate() {
			return false
		}
		p := 0
		s.t.m = lookupMonth(tok, &p)
		return true
	}},
	{[]string{"tzcorrection", "tz"}, func(s *dateScanner, tok []byte) bool {
		if !s.haveTZ() {
			return false
		}
		p := 0
		eatSpaces(tok, &p)
		z, dst, notFound := parseZone(tok, &p, s.t)
		s.t.z = z
		_ = dst
		if notFound {
			s.addError("The timezone could not be found in the database")
		}
		return true
	}},
	{[]string{"dateshortwithtimeshort12", "dateshortwithtimelong12"}, func(s *dateScanner, tok []byte) bool {
		if !s.haveDate() {
			return false
		}
		p := 0
		s.t.m = getMonth(tok, &p)
		s.t.d = getNr(tok, &p, 2)
		if !s.haveTime() {
			return false
		}
		s.t.h = getNr(tok, &p, 2)
		s.t.i = getNr(tok, &p, 2)
		if tok[p] == ':' || tok[p] == '.' {
			s.t.s = getNr(tok, &p, 2)
			if tok[p] == '.' {
				s.t.us = getFracNr(tok, &p)
			}
		}
		s.t.h += meridian(tok, &p, s.t.h)
		return true
	}},
	{[]string{"dateshortwithtimeshort", "dateshortwithtimelong", "dateshortwithtimelongtz"}, func(s *dateScanner, tok []byte) bool {
		if !s.haveDate() {
			return false
		}
		p := 0
		s.t.m = getMonth(tok, &p)
		s.t.d = getNr(tok, &p, 2)
		if !s.haveTime() {
			return false
		}
		s.t.h = getNr(tok, &p, 2)
		s.t.i = getNr(tok, &p, 2)
		if tok[p] == ':' {
			s.t.s = getNr(tok, &p, 2)
			if tok[p] == '.' {
				s.t.us = getFracNr(tok, &p)
			}
		}
		if tok[p] != 0 {
			s.parseZoneInto(tok, &p)
		}
		return true
	}},
	{[]string{"relative"}, func(s *dateScanner, tok []byte) bool {
		s.t.haveRelative = true
		p := 0
		for tok[p] != 0 {
			i := s.getSignedNr(tok, &p, 24)
			eatSpaces(tok, &p)
			s.setRelative(tok, &p, i, 1, true)
		}
		return true
	}},
	{[]string{"=[.,]"}, func(s *dateScanner, tok []byte) bool { return true }},
	{[]string{"space"}, func(s *dateScanner, tok []byte) bool { return true }},
	{[]string{"=[\\x00\\n]"}, func(s *dateScanner, tok []byte) bool { return true }},
}

func (s *dateScanner) setEpoch() {
	s.t.y, s.t.m, s.t.d = 1970, 1, 1
	s.t.h, s.t.i, s.t.s, s.t.us = 0, 0, 0, 0
	s.t.isLocaltime = true
	s.t.zoneType = zoneTypeOffset
	s.t.z = 0
	s.t.dst = 0
}

func (s *dateScanner) haveTime() bool {
	if s.t.haveTime != 0 {
		s.addError("Double time specification")
		return false
	}
	s.t.haveTime = 1
	s.t.h, s.t.i, s.t.s, s.t.us = 0, 0, 0, 0
	return true
}

func (s *dateScanner) unhaveTime() {
	s.t.haveTime = 0
	s.t.h, s.t.i, s.t.s, s.t.us = 0, 0, 0, 0
}

func (s *dateScanner) haveDate() bool {
	if s.t.haveDate != 0 {
		s.addError("Double date specification")
		return false
	}
	s.t.haveDate = 1
	return true
}

func (s *dateScanner) unhaveDate() {
	s.t.haveDate = 0
	s.t.d, s.t.m, s.t.y = 0, 0, 0
}

func (s *dateScanner) haveTZ() bool {
	if s.t.haveZone != 0 {
		if s.t.haveZone > 1 {
			s.addError("Double timezone specification")
		} else {
			s.addWarning("Double timezone specification")
		}
		s.t.haveZone++
		return false
	}
	s.t.haveZone++
	return true
}

func (s *dateScanner) parseZoneInto(tok []byte, p *int) {
	z, _, notFound := parseZone(tok, p, s.t)
	s.t.z = z
	if notFound {
		s.addError("The timezone could not be found in the database")
	}
}

func processYear(y int64, length int) int64 {
	if y == tlUnset || length >= 4 {
		return y
	}
	if y < 100 {
		if y < 70 {
			return y + 2000
		}
		return y + 1900
	}
	return y
}

func getNrEx(tok []byte, p *int, maxLength int, scanned *int) int64 {
	for tok[*p] < '0' || tok[*p] > '9' {
		if tok[*p] == 0 {
			return tlUnset
		}
		*p++
	}
	begin := *p
	length := 0
	for tok[*p] >= '0' && tok[*p] <= '9' && length < maxLength {
		*p++
		length++
	}
	if scanned != nil {
		*scanned = *p - begin
	}
	n, _ := strconv.ParseInt(string(tok[begin:*p]), 10, 64)
	return n
}

func getNr(tok []byte, p *int, maxLength int) int64 {
	return getNrEx(tok, p, maxLength, nil)
}

func skipDaySuffix(tok []byte, p *int) {
	if isCSpace(tok[*p]) {
		return
	}
	if *p+1 < len(tok) {
		w := strings.ToLower(string(tok[*p : *p+2]))
		if w == "nd" || w == "rd" || w == "st" || w == "th" {
			*p += 2
		}
	}
}

func getFracNr(tok []byte, p *int) int64 {
	for tok[*p] != '.' && tok[*p] != ':' && (tok[*p] < '0' || tok[*p] > '9') {
		if tok[*p] == 0 {
			return tlUnset
		}
		*p++
	}
	begin := *p
	for tok[*p] == '.' || tok[*p] == ':' || tok[*p] >= '0' && tok[*p] <= '9' {
		*p++
	}
	f, _ := strconv.ParseFloat(leadingFloat(string(tok[begin+1:*p])), 64)
	return int64(f * math.Pow(10, float64(7-(*p-begin))))
}

// leadingFloat keeps the prefix strtod would read.
func leadingFloat(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	}
	if i == 0 {
		return "0"
	}
	return s[:i]
}

func (s *dateScanner) getSignedNr(tok []byte, p *int, maxLength int) int64 {
	for (tok[*p] < '0' || tok[*p] > '9') && tok[*p] != '+' && tok[*p] != '-' {
		if tok[*p] == 0 {
			s.addError("Found unexpected data")
			return 0
		}
		*p++
	}
	neg := false
	for tok[*p] == '+' || tok[*p] == '-' {
		if tok[*p] == '-' {
			neg = !neg
		}
		*p++
	}
	for tok[*p] < '0' || tok[*p] > '9' {
		if tok[*p] == 0 {
			s.addError("Found unexpected data")
			return 0
		}
		*p++
	}
	begin := *p
	length := 0
	for tok[*p] >= '0' && tok[*p] <= '9' && length < maxLength {
		*p++
		length++
	}
	digits := string(tok[begin:*p])
	if neg {
		digits = "-" + digits
	}
	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		s.addError("Number out of range")
		return 0
	}
	return n
}

func meridian(tok []byte, p *int, h int64) int64 {
	var ret int64
	for tok[*p] != 0 && strings.IndexByte("AaPp", tok[*p]) < 0 {
		*p++
	}
	if tok[*p] == 'a' || tok[*p] == 'A' {
		if h == 12 {
			ret = -12
		}
	} else if h != 12 {
		ret = 12
	}
	*p++
	if tok[*p] == '.' {
		*p++
	}
	if tok[*p] == 'M' || tok[*p] == 'm' {
		*p++
	}
	if tok[*p] == '.' {
		*p++
	}
	return ret
}

func isLetter(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'
}

var relTextLookup = []struct {
	name     string
	behavior int
	value    int64
}{
	{"first", 0, 1}, {"next", 0, 1}, {"second", 0, 2}, {"third", 0, 3}, {"fourth", 0, 4}, {"fifth", 0, 5},
	{"sixth", 0, 6}, {"seventh", 0, 7}, {"eight", 0, 8}, {"eighth", 0, 8}, {"ninth", 0, 9}, {"tenth", 0, 10},
	{"eleventh", 0, 11}, {"twelfth", 0, 12}, {"last", 0, -1}, {"previous", 0, -1}, {"this", 1, 0},
}

func getRelativeText(tok []byte, p *int, behavior *int) int64 {
	for tok[*p] == ' ' || tok[*p] == '\t' || tok[*p] == '-' || tok[*p] == '/' {
		*p++
	}
	begin := *p
	for isLetter(tok[*p]) {
		*p++
	}
	word := strings.ToLower(string(tok[begin:*p]))
	var value int64
	for _, e := range relTextLookup {
		if word == e.name {
			value = e.value
			*behavior = e.behavior
		}
	}
	return value
}

var monthLookup = map[string]int64{
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6, "jul": 7, "aug": 8, "sep": 9, "sept": 9, "oct": 10,
	"nov": 11, "dec": 12, "i": 1, "ii": 2, "iii": 3, "iv": 4, "v": 5, "vi": 6, "vii": 7, "viii": 8, "ix": 9, "x": 10,
	"xi": 11, "xii": 12, "january": 1, "february": 2, "march": 3, "april": 4, "june": 6, "july": 7, "august": 8,
	"september": 9, "october": 10, "november": 11, "december": 12,
}

func lookupMonth(tok []byte, p *int) int64 {
	begin := *p
	for isLetter(tok[*p]) {
		*p++
	}
	return monthLookup[strings.ToLower(string(tok[begin:*p]))]
}

func getMonth(tok []byte, p *int) int64 {
	for tok[*p] == ' ' || tok[*p] == '\t' || tok[*p] == '-' || tok[*p] == '.' || tok[*p] == '/' {
		*p++
	}
	return lookupMonth(tok, p)
}

func eatSpaces(tok []byte, p *int) {
	for {
		switch {
		case tok[*p] == ' ' || tok[*p] == '\t':
			*p++
		case *p+2 < len(tok) && tok[*p] == 0xe2 && tok[*p+1] == 0x80 && tok[*p+2] == 0xaf:
			*p += 3
		case *p+1 < len(tok) && tok[*p] == 0xc2 && tok[*p+1] == 0xa0:
			*p += 2
		default:
			return
		}
	}
}

const (
	unitMicrosec = iota + 1
	unitSecond
	unitMinute
	unitHour
	unitDay
	unitMonth
	unitYear
	unitWeekday
	unitSpecial
)

type relunit struct {
	unit       int
	multiplier int64
}

var relunitLookup = map[string]relunit{
	"ms": {unitMicrosec, 1000}, "msec": {unitMicrosec, 1000}, "msecs": {unitMicrosec, 1000},
	"millisecond": {unitMicrosec, 1000}, "milliseconds": {unitMicrosec, 1000},
	"µs": {unitMicrosec, 1}, "usec": {unitMicrosec, 1}, "usecs": {unitMicrosec, 1}, "µsec": {unitMicrosec, 1},
	"µsecs": {unitMicrosec, 1}, "microsecond": {unitMicrosec, 1}, "microseconds": {unitMicrosec, 1},
	"sec": {unitSecond, 1}, "secs": {unitSecond, 1}, "second": {unitSecond, 1}, "seconds": {unitSecond, 1},
	"min": {unitMinute, 1}, "mins": {unitMinute, 1}, "minute": {unitMinute, 1}, "minutes": {unitMinute, 1},
	"hour": {unitHour, 1}, "hours": {unitHour, 1}, "day": {unitDay, 1}, "days": {unitDay, 1},
	"week": {unitDay, 7}, "weeks": {unitDay, 7}, "fortnight": {unitDay, 14}, "fortnights": {unitDay, 14},
	"forthnight": {unitDay, 14}, "forthnights": {unitDay, 14}, "month": {unitMonth, 1}, "months": {unitMonth, 1},
	"year": {unitYear, 1}, "years": {unitYear, 1},
	"mondays": {unitWeekday, 1}, "monday": {unitWeekday, 1}, "mon": {unitWeekday, 1},
	"tuesdays": {unitWeekday, 2}, "tuesday": {unitWeekday, 2}, "tue": {unitWeekday, 2},
	"wednesdays": {unitWeekday, 3}, "wednesday": {unitWeekday, 3}, "wed": {unitWeekday, 3},
	"thursdays": {unitWeekday, 4}, "thursday": {unitWeekday, 4}, "thu": {unitWeekday, 4},
	"fridays": {unitWeekday, 5}, "friday": {unitWeekday, 5}, "fri": {unitWeekday, 5},
	"saturdays": {unitWeekday, 6}, "saturday": {unitWeekday, 6}, "sat": {unitWeekday, 6},
	"sundays": {unitWeekday, 0}, "sunday": {unitWeekday, 0}, "sun": {unitWeekday, 0},
	"weekday": {unitSpecial, specialWeekday}, "weekdays": {unitSpecial, specialWeekday},
}

func lookupRelunit(tok []byte, p *int) *relunit {
	begin := *p
	for tok[*p] != 0 && strings.IndexByte(" ,\t;:/.-()", tok[*p]) < 0 {
		*p++
	}
	u, ok := relunitLookup[strings.ToLower(string(tok[begin:*p]))]
	if !ok {
		return nil
	}
	return &u
}

func (s *dateScanner) addOverflow(e *int64, amount, mult int64) {
	p, ok := mulInt(amount, mult)
	if ok {
		sum := *e + p
		if (sum > *e) == (p > 0) || p == 0 {
			*e = sum
			return
		}
	}
	*e += amount * mult
	s.addError("Number out of range")
}

func (s *dateScanner) setRelative(tok []byte, p *int, amount int64, behavior int, keepTime bool) {
	u := lookupRelunit(tok, p)
	if u == nil {
		return
	}
	r := &s.t.rel
	switch u.unit {
	case unitMicrosec:
		s.addOverflow(&r.us, amount, u.multiplier)
	case unitSecond:
		s.addOverflow(&r.s, amount, u.multiplier)
	case unitMinute:
		s.addOverflow(&r.i, amount, u.multiplier)
	case unitHour:
		s.addOverflow(&r.h, amount, u.multiplier)
	case unitDay:
		s.addOverflow(&r.d, amount, u.multiplier)
	case unitMonth:
		s.addOverflow(&r.m, amount, u.multiplier)
	case unitYear:
		s.addOverflow(&r.y, amount, u.multiplier)
	case unitWeekday:
		s.t.haveRelative = true
		r.haveWeekdayRelative = true
		if !keepTime {
			s.unhaveTime()
		}
		if amount > 0 {
			r.d += (amount - 1) * 7
		} else {
			r.d += amount * 7
		}
		r.weekday = u.multiplier
		r.weekdayBehavior = behavior
	case unitSpecial:
		s.t.haveRelative = true
		r.haveSpecialRelative = true
		if !keepTime {
			s.unhaveTime()
		}
		r.specialType = int(u.multiplier)
		r.specialAmount = amount
	}
}

// parseZone is timelib_parse_zone: an offset ("+02:00", "GMT-5"), an abbreviation ("EST") or a zone identifier
// ("Europe/Paris"); it returns the offset in seconds, the DST flag and whether nothing was found.
func parseZone(tok []byte, p *int, t *tlTime) (int64, int, bool) {
	parens := 0
	for tok[*p] == ' ' || tok[*p] == '\t' || tok[*p] == '(' {
		if tok[*p] == '(' {
			parens++
		}
		*p++
	}
	if *p+3 < len(tok) && tok[*p] == 'G' && tok[*p+1] == 'M' && tok[*p+2] == 'T' && (tok[*p+3] == '+' || tok[*p+3] == '-') {
		*p += 3
	}
	var ret int64
	dst := 0
	notFound := false
	if tok[*p] == '+' || tok[*p] == '-' {
		neg := tok[*p] == '-'
		*p++
		t.isLocaltime = true
		t.zoneType = zoneTypeOffset
		t.dst = 0
		var nf bool
		ret, nf = parseTZCor(tok, p)
		notFound = nf
		if neg {
			ret = -ret
		}
	} else {
		t.isLocaltime = true
		begin := *p
		for c := tok[*p]; isLetter(c) || c >= '0' && c <= '9' || c == '/' || c == '_' || c == '-' || c == '+'; c = tok[*p] {
			*p++
		}
		word := string(tok[begin:*p])
		found := 0
		if len(word) < 6 {
			if a, ok := abbrSearch(word); ok {
				ret = a.offset - int64(a.dst)*3600
				dst = a.dst
				found = 1
				t.zoneType = zoneTypeAbbr
				t.dst = int64(dst)
				t.tzAbbr = strings.ToUpper(word)
			}
		}
		if found == 0 || word == "UTC" {
			if tz, ok := LoadTimeZone(word); ok {
				t.tz = tz
				t.zoneType = zoneTypeID
				found++
			}
		}
		notFound = found == 0
	}
	for parens > 0 && tok[*p] == ')' {
		*p++
		parens--
	}
	return ret, dst, notFound
}

func abbrSearch(word string) (tzAbbr, bool) {
	w := strings.ToLower(word)
	if w == "utc" || w == "gmt" {
		return tzAbbr{0, 0}, true
	}
	a, ok := tzAbbreviations[w]
	return a, ok
}

func parseTZCor(tok []byte, p *int) (int64, bool) {
	begin := *p
	for tok[*p] >= '0' && tok[*p] <= '9' || tok[*p] == ':' {
		*p++
	}
	s := string(tok[begin:*p])
	atoi := func(x string) int64 {
		n, _ := strconv.ParseInt(leadingDigits(x), 10, 64)
		return n
	}
	switch len(s) {
	case 1, 2:
		return atoi(s) * 3600, false
	case 3, 4:
		if s[1] == ':' {
			return atoi(s)*3600 + atoi(s[2:])*60, false
		}
		if s[2] == ':' {
			return atoi(s)*3600 + atoi(s[3:])*60, false
		}
		n := atoi(s)
		return n/100*3600 + n%100*60, false
	case 5:
		if s[2] != ':' {
			break
		}
		return atoi(s)*3600 + atoi(s[3:])*60, false
	case 6:
		n := atoi(s)
		return n/10000*3600 + (n/100)%100*60 + n%100, false
	case 8:
		if s[2] != ':' || s[5] != ':' {
			break
		}
		return atoi(s)*3600 + atoi(s[3:])*60 + atoi(s[6:]), false
	}
	return 0, true
}

func leadingDigits(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return "0"
	}
	return s[:i]
}

// strtotime is timelib_strtotime: it scans the string into a tlTime with errors and warnings.
func strtotime(input string) (*tlTime, []tlError, []tlError) {
	t := &tlTime{y: tlUnset, m: tlUnset, d: tlUnset, h: tlUnset, i: tlUnset, s: tlUnset, us: tlUnset, z: tlUnset, dst: tlUnset}
	// Leading and trailing whitespace is trimmed.
	start, end := 0, len(input)
	for start < end-1 && isCSpace(input[start]) {
		start++
	}
	for end-1 > start && isCSpace(input[end-1]) {
		end--
	}
	str := input[start:end]
	if len(str) == 0 || (len(str) == 1 && isCSpace(str[0])) {
		s := &dateScanner{str: []byte{0}, t: t}
		s.addError("Empty string")
		return t, s.errors, nil
	}
	s := &dateScanner{str: append([]byte(str), make([]byte, 32)...), n: len(str), t: t}
	text := string(s.str)
	cur := 0
	for cur < s.n {
		s.tok = cur
		best, bestLen := -1, 0
		for i, r := range dateRules {
			if loc := r.re.FindStringIndex(text[cur:]); loc != nil && loc[1] > bestLen {
				best, bestLen = i, loc[1]
			}
		}
		if best >= 0 {
			tok := append([]byte(nil), s.str[cur:cur+bestLen]...)
			tok = append(tok, 0, 0, 0, 0)
			cur += bestLen
			dateRules[best].action(s, tok)
			continue
		}
		// No rule matches: "any".
		s.addError("Unexpected character")
		cur++
	}
	if t.haveTime != 0 && !validTime(t.h, t.i, t.s) {
		s.tok = s.n
		s.addWarning("The parsed time was invalid")
	}
	if t.haveDate != 0 && !validDate(t.y, t.m, t.d) {
		s.tok = s.n
		s.addWarning("The parsed date was invalid")
	}
	return t, s.errors, s.warnings
}

func validTime(h, i, s int64) bool {
	return !(h < 0 || h > 23 || i < 0 || i > 59 || s < 0 || s > 59)
}

func validDate(y, m, d int64) bool {
	return !(m < 1 || m > 12 || d < 1 || d > daysInMonth(y, m))
}
