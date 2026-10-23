// This file is part of the Go port of Twig.
//
// (c) Fabien Potencier
//
// For the full copyright and license information, please view the LICENSE
// file that was distributed with this source code.

package php

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
	// The zone database is embedded so that dates do not depend on the host's zoneinfo files.
	_ "time/tzdata"
)

// TimeZoneInfo is a zone of the time zone database.
type TimeZoneInfo struct {
	Name string
	loc  *time.Location
}

var zoneCache sync.Map

// LoadTimeZone looks a zone identifier up, ignoring case like timelib does.
func LoadTimeZone(name string) (*TimeZoneInfo, bool) {
	if name == "" {
		return nil, false
	}
	// The zone keeps the name as it was written ("asia/tokyo"), like timelib.
	key := name
	if z, ok := zoneCache.Load(key); ok {
		tz, _ := z.(*TimeZoneInfo)
		return tz, tz != nil
	}
	var tz *TimeZoneInfo
	for _, candidate := range zoneNameCandidates(name) {
		if strings.ContainsAny(candidate, ".\\") || strings.HasPrefix(candidate, "/") {
			continue
		}
		if loc, err := time.LoadLocation(candidate); err == nil && candidate != "Local" {
			tz = &TimeZoneInfo{Name: name, loc: loc}
			break
		}
	}
	zoneCache.Store(key, tz)
	return tz, tz != nil
}

// zoneNameCandidates spells a case-insensitive zone name the ways the database uses ("america/new_york" ->
// "America/New_York", "utc" -> "UTC").
func zoneNameCandidates(name string) []string {
	out := []string{name}
	if u := strings.ToUpper(name); u != name {
		out = append(out, u)
	}
	parts := strings.Split(name, "/")
	for i, p := range parts {
		words := strings.Split(p, "_")
		for j, w := range words {
			sub := strings.Split(w, "-")
			for k, x := range sub {
				if x != "" {
					sub[k] = strings.ToUpper(x[:1]) + strings.ToLower(x[1:])
				}
			}
			words[j] = strings.Join(sub, "-")
		}
		parts[i] = strings.Join(words, "_")
	}
	out = append(out, strings.Join(parts, "/"))
	return out
}

// offsetInfo is timelib_get_time_zone_offset_info: the UTC offset at ts, the start of that offset's period
// (math.MinInt64 when there is none), DST and the abbreviation.
func (z *TimeZoneInfo) offsetInfo(ts int64) (offset int64, transition int64, dst bool, abbr string) {
	t := time.Unix(ts, 0).In(z.loc)
	name, off := t.Zone()
	start, _ := t.ZoneBounds()
	transition = math.MinInt64
	if !start.IsZero() {
		transition = start.Unix()
	}
	return int64(off), transition, t.IsDST(), name
}

func isLeap(y int64) bool {
	return y%4 == 0 && (y%100 != 0 || y%400 == 0)
}

var daysInMonthTable = [13]int64{31, 31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
var daysInMonthLeapTable = [13]int64{31, 31, 29, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}

func daysInMonth(y, m int64) int64 {
	if m < 1 || m > 12 {
		return 0
	}
	if isLeap(y) {
		return daysInMonthLeapTable[m]
	}
	return daysInMonthTable[m]
}

func positiveMod(x, y int64) int64 {
	r := x % y
	if r < 0 {
		r += y
	}
	return r
}

// dayOfWeek is timelib_day_of_week (0 = Sunday).
func dayOfWeek(y, m, d int64) int64 {
	days := epochDays(y, m, d)
	return positiveMod(days+4, 7)
}

func isoDayOfWeek(y, m, d int64) int64 {
	w := dayOfWeek(y, m, d)
	if w == 0 {
		return 7
	}
	return w
}

var dayOfYearTable = [13]int64{-1, 0, 31, 59, 90, 120, 151, 181, 212, 243, 273, 304, 334}
var dayOfYearLeapTable = [13]int64{-1, 0, 31, 60, 91, 121, 152, 182, 213, 244, 274, 305, 335}

func dayOfYear(y, m, d int64) int64 {
	if isLeap(y) {
		return dayOfYearLeapTable[m] + d - 1
	}
	return dayOfYearTable[m] + d - 1
}

// isoWeek is timelib_isoweek_from_date.
func isoWeek(y, m, d int64) (week, year int64) {
	yLeap := isLeap(y)
	prevLeap := isLeap(y - 1)
	doy := dayOfYear(y, m, d) + 1
	if yLeap && m > 2 {
		doy++
	}
	jan1 := dayOfWeek(y, 1, 1)
	weekday := dayOfWeek(y, m, d)
	if weekday == 0 {
		weekday = 7
	}
	if jan1 == 0 {
		jan1 = 7
	}
	// Find if y M D falls in YearNumber y-1, WeekNumber 52 or 53.
	if doy <= (8-jan1) && jan1 > 4 {
		year = y - 1
		if jan1 == 5 || (jan1 == 6 && prevLeap) {
			week = 53
		} else {
			week = 52
		}
	} else {
		year = y
	}
	// Find if y M D falls in YearNumber y+1, WeekNumber 1.
	if year == y {
		i := int64(365)
		if yLeap {
			i = 366
		}
		if (i - (doy - boolI64(yLeap))) < (4 - weekday) {
			year = y + 1
			week = 1
			return
		}
	}
	// Find if y M D falls in YearNumber y, WeekNumber 1 through 53.
	if year == y {
		j := doy + (7 - weekday) + (jan1 - 1)
		week = j / 7
		if jan1 > 4 {
			week--
		}
	}
	return
}

func boolI64(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// daynrFromWeeknr is timelib_daynr_from_weeknr.
func daynrFromWeeknr(iy, iw, id int64) int64 {
	dow := dayOfWeek(iy, 1, 1)
	day := 0 - dow
	if dow > 4 {
		day += 7
	}
	return day + ((iw - 1) * 7) + id
}

// epochDays is the number of days from 1970-01-01 (timelib_epoch_days_from_time).
func epochDays(y, m, d int64) int64 {
	if m <= 2 {
		y--
	}
	era := y
	if y < 0 {
		era = y - 399
	}
	era /= 400
	yoe := y - era*400
	mm := m - 3
	if m <= 2 {
		mm = m + 9
	}
	doy := (153*mm+2)/5 + d - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return era*146097 + doe - 719468
}

// unixtimeToDate is timelib_unixtime2date.
func unixtimeToDate(ts int64) (y, m, d int64) {
	days := ts / 86400
	if ts%86400 < 0 {
		days--
	}
	days += 719468
	era := days
	if days < 0 {
		era = days - 146096
	}
	era /= 146097
	doe := days - era*146097
	yoe := (doe - doe/1460 + doe/36524 - doe/146096) / 365
	y = yoe + era*400
	doy := doe - (365*yoe + yoe/4 - yoe/100)
	mp := (5*doy + 2) / 153
	d = doy - (153*mp+2)/5 + 1
	if mp < 10 {
		m = mp + 3
	} else {
		m = mp - 9
	}
	if m <= 2 {
		y++
	}
	return
}

func doRangeLimit(start, end, adj int64, a, b *int64) {
	if *a < start {
		ap1 := *a + 1
		*b -= (start-ap1)/adj + 1
		*a += adj * ((start - ap1) / adj)
		*a += adj
	}
	if *a >= end {
		*b += *a / adj
		*a -= adj * (*a / adj)
	}
}

func doRangeLimitDays(y, m, d *int64) bool {
	const daysPerEra, yearsPerEra = 146097, 400
	if *d >= daysPerEra || *d <= -daysPerEra {
		*y += yearsPerEra * (*d / daysPerEra)
		*d -= daysPerEra * (*d / daysPerEra)
	}
	doRangeLimit(1, 13, 12, m, y)
	table := &daysInMonthTable
	if isLeap(*y) {
		table = &daysInMonthLeapTable
	}
	ret := false
	for *d <= 0 && *m > 0 {
		pm, py := *m-1, *y
		if pm < 1 {
			pm += 12
			py--
		}
		*d += daysInMonth(py, pm)
		*m--
		ret = true
	}
	for *d > 0 && *m <= 12 && *d > table[*m] {
		*d -= table[*m]
		*m++
		ret = true
	}
	return ret
}

func (t *tlTime) normalize() {
	if t.us != tlUnset {
		doRangeLimit(0, 1000000, 1000000, &t.us, &t.s)
	}
	if t.s != tlUnset {
		doRangeLimit(0, 60, 60, &t.s, &t.i)
		doRangeLimit(0, 60, 60, &t.i, &t.h)
		doRangeLimit(0, 24, 24, &t.h, &t.d)
	}
	doRangeLimit(1, 13, 12, &t.m, &t.y)
	if t.y == 1970 && t.m == 1 && t.d != 1 {
		t.magicDateCalc()
	}
	for doRangeLimitDays(&t.y, &t.m, &t.d) {
	}
	doRangeLimit(1, 13, 12, &t.m, &t.y)
}

func (t *tlTime) magicDateCalc() {
	if t.d < -719498 {
		return
	}
	g := t.d + 719468 - 1
	y := (10000*g + 14780) / 3652425
	ddd := g - ((365 * y) + (y / 4) - (y / 100) + (y / 400))
	if ddd < 0 {
		y--
		ddd = g - ((365 * y) + (y / 4) - (y / 100) + (y / 400))
	}
	mi := (100*ddd + 52) / 3060
	mm := ((mi + 2) % 12) + 1
	y = y + (mi+2)/12
	dd := ddd - ((mi*306 + 5) / 10) + 1
	t.y, t.m, t.d = y, mm, dd
}

func (t *tlTime) adjustForWeekday() {
	r := &t.rel
	current := dayOfWeek(t.y, t.m, t.d)
	if r.weekdayBehavior == 2 {
		if current == 0 && r.weekday != 0 {
			r.weekday -= 7
		}
		if r.weekday == 0 && current != 0 {
			r.weekday = 7
		}
		t.d -= current
		t.d += r.weekday
		return
	}
	diff := r.weekday - current
	if (r.d < 0 && diff < 0) || (r.d >= 0 && diff <= -int64(r.weekdayBehavior)) {
		diff += 7
	}
	if r.weekday >= 0 {
		t.d += diff
	} else {
		w := r.weekday
		if w < 0 {
			w = -w
		}
		t.d -= 7 - (w - current)
	}
	r.haveWeekdayRelative = false
}

func (t *tlTime) adjustRelative() {
	if t.rel.haveWeekdayRelative {
		t.adjustForWeekday()
	}
	t.normalize()
	if t.haveRelative {
		t.us += t.rel.us
		t.s += t.rel.s
		t.i += t.rel.i
		t.h += t.rel.h
		t.d += t.rel.d
		t.m += t.rel.m
		t.y += t.rel.y
	}
	switch t.rel.firstLastDayOf {
	case specialFirstDayOfMonth:
		t.d = 1
	case specialLastDayOfMonth:
		t.d = 0
		t.m++
	}
	t.normalize()
}

func (t *tlTime) adjustSpecialWeekday() {
	count := t.rel.specialAmount
	dow := dayOfWeek(t.y, t.m, t.d)
	t.d += (count / 5) * 7
	rem := count % 5
	if count > 0 {
		if rem == 0 {
			if dow == 0 {
				t.d -= 2
			} else if dow == 6 {
				t.d--
			}
		} else if dow == 6 {
			t.d++
		} else if dow+rem > 5 {
			t.d += 2
		}
	} else {
		if rem == 0 {
			if dow == 6 {
				t.d += 2
			} else if dow == 0 {
				t.d++
			}
		} else if dow == 0 {
			t.d--
		} else if dow+rem < 1 {
			t.d -= 2
		}
	}
	t.d += rem
}

func (t *tlTime) adjustSpecial() {
	if t.rel.haveSpecialRelative && t.rel.specialType == specialWeekday {
		t.adjustSpecialWeekday()
	}
	t.normalize()
	t.rel.specialType, t.rel.specialAmount = 0, 0
}

func (t *tlTime) adjustSpecialEarly() {
	if t.rel.haveSpecialRelative {
		switch t.rel.specialType {
		case specialDayOfWeekInMonth:
			t.d = 1
			t.m += t.rel.m
			t.rel.m = 0
		case specialLastDayOfWeekInMonth:
			t.d = 1
			t.m += t.rel.m + 1
			t.rel.m = 0
		}
	}
	switch t.rel.firstLastDayOf {
	case specialFirstDayOfMonth:
		t.d = 1
	case specialLastDayOfMonth:
		t.d = 0
		t.m++
	}
	t.normalize()
}

func (t *tlTime) adjustTimezone(tzi *TimeZoneInfo) {
	switch t.zoneType {
	case zoneTypeOffset:
		t.isLocaltime = true
		t.sse += -t.z
		return
	case zoneTypeAbbr:
		t.isLocaltime = true
		t.sse += -t.z - t.dst*3600
		return
	case zoneTypeID:
		tzi = t.tz
	}
	if tzi == nil {
		return
	}
	currentOffset, _, currentDST, _ := tzi.offsetInfo(t.sse)
	afterOffset, afterTransition, _, _ := tzi.offsetInfo(t.sse - currentOffset)
	actualOffset, actualTransition := afterOffset, afterTransition
	if currentOffset == afterOffset && t.haveZone != 0 {
		if currentOffset >= 0 && t.dst != 0 && !currentDST {
			earlierOffset, earlierTransition, _, _ := tzi.offsetInfo(t.sse - currentOffset - 7200)
			if earlierOffset != afterOffset && t.sse-earlierOffset < afterTransition {
				actualOffset, actualTransition = earlierOffset, earlierTransition
			}
		} else if currentOffset <= 0 && currentDST && t.dst == 0 {
			laterOffset, laterTransition, _, _ := tzi.offsetInfo(t.sse - currentOffset + 7200)
			if laterOffset != afterOffset && t.sse-laterOffset >= laterTransition {
				actualOffset, actualTransition = laterOffset, laterTransition
			}
		}
	}
	t.isLocaltime = true
	inTransition := actualTransition != math.MinInt64 &&
		t.sse-actualOffset >= actualTransition+(currentOffset-actualOffset) &&
		t.sse-actualOffset < actualTransition
	if currentOffset != actualOffset && !inTransition {
		t.sse -= actualOffset
	} else {
		t.sse -= currentOffset
	}
	t.setTimezone(tzi)
}

// updateTS is timelib_update_ts.
func (t *tlTime) updateTS(tzi *TimeZoneInfo) {
	t.adjustSpecialEarly()
	t.adjustRelative()
	t.adjustSpecial()
	t.sse = t.h*3600 + t.i*60 + t.s
	days := epochDays(t.y, t.m, t.d)
	t.sse += days * 43200
	t.sse += days * 43200
	t.adjustTimezone(tzi)
	t.haveRelative = false
	t.rel.haveWeekdayRelative, t.rel.haveSpecialRelative = false, false
	t.rel.firstLastDayOf = 0
}

func (t *tlTime) unixtimeToGMT(ts int64) {
	t.y, t.m, t.d = unixtimeToDate(ts)
	rem := ts % 86400
	if rem < 0 {
		rem += 86400
	}
	t.h = rem / 3600
	t.i = (rem - t.h*3600) / 60
	t.s = rem % 60
	t.z, t.dst = 0, 0
	t.sse = ts
	t.isLocaltime = false
}

func (t *tlTime) updateFromSSE() {
	sse, z, dst := t.sse, t.z, t.dst
	switch t.zoneType {
	case zoneTypeAbbr, zoneTypeOffset:
		t.unixtimeToGMT(t.sse + t.z + t.dst*3600)
	case zoneTypeID:
		off, _, _, _ := t.tz.offsetInfo(t.sse)
		t.unixtimeToGMT(t.sse + off)
	default:
		t.unixtimeToGMT(t.sse)
	}
	t.sse, t.z, t.dst = sse, z, dst
	t.isLocaltime = true
	t.haveZone = 1
}

func (t *tlTime) unixtimeToLocal(ts int64) {
	switch t.zoneType {
	case zoneTypeAbbr, zoneTypeOffset:
		z, dst := t.z, t.dst
		t.unixtimeToGMT(ts + t.z + t.dst*3600)
		t.sse, t.z, t.dst = ts, z, dst
	case zoneTypeID:
		off, _, isDST, abbr := t.tz.offsetInfo(ts)
		tz := t.tz
		t.unixtimeToGMT(ts + off)
		t.sse = ts
		t.dst = boolI64(isDST)
		t.z = off
		t.tz = tz
		t.tzAbbr = strings.ToUpper(abbr)
	default:
		t.isLocaltime = false
		t.haveZone = 0
		return
	}
	t.isLocaltime = true
	t.haveZone = 1
}

func (t *tlTime) setTimezone(tz *TimeZoneInfo) {
	off, _, isDST, abbr := tz.offsetInfo(t.sse)
	t.z = off
	t.dst = boolI64(isDST)
	t.tz = tz
	t.tzAbbr = strings.ToUpper(abbr)
	t.haveZone = 1
	t.zoneType = zoneTypeID
}

// DateTimeZone is PHP's DateTimeZone: a zone identifier, a UTC offset or an abbreviation.
type DateTimeZone struct {
	zoneType int
	tz       *TimeZoneInfo
	offset   int64
	dst      int64
	abbr     string
}

// ClassName implements Object.
func (*DateTimeZone) ClassName() string { return "DateTimeZone" }

// Name is DateTimeZone::getName().
func (z *DateTimeZone) Name() string {
	switch z.zoneType {
	case zoneTypeID:
		return z.tz.Name
	case zoneTypeAbbr:
		return z.abbr
	}
	return formatOffset(z.offset, true)
}

func formatOffset(off int64, colon bool) string {
	sign := byte('+')
	if off < 0 {
		sign = '-'
	}
	a := off
	if a < 0 {
		a = -a
	}
	if colon {
		return fmt.Sprintf("%c%02d:%02d", sign, a/3600, (a%3600)/60)
	}
	return fmt.Sprintf("%c%02d%02d", sign, a/3600, (a%3600)/60)
}

// NewDateTimeZone is new DateTimeZone($name); it throws DateInvalidTimeZoneException for unknown zones.
func NewDateTimeZone(name string) (*DateTimeZone, error) {
	bad := func(msg string) error {
		return Throw("DateInvalidTimeZoneException", "DateTimeZone::__construct(): %s", msg)
	}
	if strings.IndexByte(name, 0) >= 0 {
		return nil, bad("Timezone must not contain null bytes")
	}
	tok := append([]byte(name), 0, 0, 0, 0)
	t := &tlTime{}
	p := 0
	z, dst, notFound := parseZone(tok, &p, t)
	if z >= 100*3600 || z <= -100*3600 {
		return nil, bad(fmt.Sprintf("Timezone offset is out of range (%s)", name))
	}
	if notFound || tok[p] != 0 {
		return nil, bad(fmt.Sprintf("Unknown or bad timezone (%s)", name))
	}
	switch t.zoneType {
	case zoneTypeID:
		return &DateTimeZone{zoneType: zoneTypeID, tz: t.tz}, nil
	case zoneTypeOffset:
		return &DateTimeZone{zoneType: zoneTypeOffset, offset: z}, nil
	}
	return &DateTimeZone{zoneType: zoneTypeAbbr, offset: z, dst: int64(dst), abbr: t.tzAbbr}, nil
}

// DateTime is PHP's DateTime.
type DateTime struct {
	t tlTime
}

// ClassName implements Object.
func (*DateTime) ClassName() string { return "DateTime" }

// Clone is clone $date.
func (d *DateTime) Clone() *DateTime {
	c := *d
	return &c
}

// Timestamp is getTimestamp().
func (d *DateTime) Timestamp() int64 { return d.t.sse }

// Before reports whether d is earlier than e (the DateTime comparison handler).
func (d *DateTime) compare(e *DateTime) int {
	switch {
	case d.t.sse < e.t.sse:
		return -1
	case d.t.sse > e.t.sse:
		return 1
	case d.t.us < e.t.us:
		return -1
	case d.t.us > e.t.us:
		return 1
	}
	return 0
}

func malformed(class, prefix, input string, errs []tlError) error {
	e := errs[0]
	c := e.char
	if c == 0 {
		c = ' '
	}
	// The character is one raw byte of the input, as PHP's %c prints it.
	return Throw(class, "%sFailed to parse time string (%s) at position %d (%s): %s", prefix, input, e.pos, string([]byte{c}), e.msg)
}

// NewDateTime is new DateTime($time, $timezone) with the process default zone UTC; tz nil means no zone
// argument. now is the current time (seconds and microseconds) used to fill what the string leaves out.
func NewDateTime(input string, tz *DateTimeZone, now time.Time) (*DateTime, error) {
	if input == "" {
		input = "now"
	}
	parsed, errs, _ := strtotime(input)
	if len(errs) > 0 {
		return nil, malformed("DateMalformedStringException", "", input, errs)
	}
	nowT := &tlTime{}
	var tzi *TimeZoneInfo
	typ := zoneTypeID
	if tz != nil {
		typ = tz.zoneType
		switch tz.zoneType {
		case zoneTypeID:
			tzi = tz.tz
		case zoneTypeOffset:
			nowT.z = tz.offset
		case zoneTypeAbbr:
			nowT.z, nowT.dst, nowT.tzAbbr = tz.offset, tz.dst, tz.abbr
		}
	} else if parsed.tz != nil {
		tzi = parsed.tz
	} else {
		tzi, _ = LoadTimeZone("UTC")
	}
	nowT.zoneType = typ
	nowT.tz = tzi
	nowT.unixtimeToLocal(now.Unix())
	nowT.us = int64(now.Nanosecond() / 1000)
	if input == "now" {
		return &DateTime{t: *nowT}, nil
	}
	fillHoles(parsed, nowT)
	parsed.updateTS(tzi)
	parsed.updateFromSSE()
	parsed.haveRelative = false
	return &DateTime{t: *parsed}, nil
}

func fillHoles(p, now *tlTime) {
	if p.haveDate != 0 && p.haveTime == 0 {
		p.h, p.i, p.s, p.us = 0, 0, 0, 0
	}
	if p.y != tlUnset || p.m != tlUnset || p.d != tlUnset || p.h != tlUnset || p.i != tlUnset || p.s != tlUnset {
		if p.us == tlUnset {
			p.us = 0
		}
	} else if p.us == tlUnset {
		p.us = orZero(now.us)
	}
	if p.y == tlUnset {
		p.y = orZero(now.y)
	}
	if p.m == tlUnset {
		p.m = orZero(now.m)
	}
	if p.d == tlUnset {
		p.d = orZero(now.d)
	}
	if p.h == tlUnset {
		p.h = orZero(now.h)
	}
	if p.i == tlUnset {
		p.i = orZero(now.i)
	}
	if p.s == tlUnset {
		p.s = orZero(now.s)
	}
	if p.tz == nil {
		p.tz = now.tz
		if p.z == tlUnset {
			p.z = orZero(now.z)
		}
		if p.dst == tlUnset {
			p.dst = orZero(now.dst)
		}
		if p.tzAbbr == "" {
			p.tzAbbr = now.tzAbbr
		}
	}
	if p.zoneType == 0 && now.zoneType != 0 {
		p.zoneType = now.zoneType
		p.isLocaltime = true
	}
}

func orZero(v int64) int64 {
	if v == tlUnset {
		return 0
	}
	return v
}

// Modify is DateTime::modify($modifier).
func (d *DateTime) Modify(modifier string) error {
	tmp, errs, _ := strtotime(modifier)
	if len(errs) > 0 {
		return malformed("DateMalformedStringException", "DateTime::modify(): ", modifier, errs)
	}
	t := &d.t
	t.rel = tmp.rel
	t.haveRelative = tmp.haveRelative
	if tmp.y != tlUnset {
		t.y = tmp.y
	}
	if tmp.m != tlUnset {
		t.m = tmp.m
	}
	if tmp.d != tlUnset {
		t.d = tmp.d
	}
	if tmp.h != tlUnset {
		t.h = tmp.h
		if tmp.i != tlUnset {
			t.i = tmp.i
			if tmp.s != tlUnset {
				t.s = tmp.s
			} else {
				t.s = 0
			}
		} else {
			t.i, t.s = 0, 0
		}
	}
	if tmp.us != tlUnset {
		t.us = tmp.us
	}
	// "@<timestamp>" resets the zone to UTC.
	if tmp.y == 1970 && tmp.m == 1 && tmp.d == 1 && tmp.h == 0 && tmp.i == 0 && tmp.s == 0 && tmp.us == 0 &&
		tmp.haveZone != 0 && tmp.zoneType == zoneTypeOffset && tmp.z == 0 && tmp.dst == 0 {
		t.tzAbbr = ""
		t.z = 0
		t.haveZone = 1
		t.zoneType = zoneTypeOffset
		t.dst = 0
		t.tz = nil
	}
	t.updateTS(nil)
	t.updateFromSSE()
	t.haveRelative = false
	t.rel = relTime{}
	return nil
}

// SetTimezone is DateTime::setTimezone($zone).
func (d *DateTime) SetTimezone(z *DateTimeZone) {
	t := &d.t
	switch z.zoneType {
	case zoneTypeOffset:
		t.tzAbbr = ""
		t.z = z.offset
		t.haveZone = 1
		t.zoneType = zoneTypeOffset
		t.dst = 0
		t.tz = nil
	case zoneTypeAbbr:
		t.tzAbbr = z.abbr
		t.z = z.offset
		t.haveZone = 1
		t.zoneType = zoneTypeAbbr
		t.dst = z.dst
		t.tz = nil
	case zoneTypeID:
		t.setTimezone(z.tz)
	}
	t.unixtimeToLocal(t.sse)
}

// Timezone is DateTime::getTimezone().
func (d *DateTime) Timezone() *DateTimeZone {
	switch d.t.zoneType {
	case zoneTypeID:
		return &DateTimeZone{zoneType: zoneTypeID, tz: d.t.tz}
	case zoneTypeAbbr:
		return &DateTimeZone{zoneType: zoneTypeAbbr, offset: d.t.z, dst: d.t.dst, abbr: d.t.tzAbbr}
	}
	return &DateTimeZone{zoneType: zoneTypeOffset, offset: d.t.z}
}

// Offset is DateTime::getOffset().
func (d *DateTime) Offset() int64 {
	off, _, _ := d.offset()
	return off
}

func (d *DateTime) offset() (offset int64, isDST bool, abbr string) {
	t := &d.t
	switch t.zoneType {
	case zoneTypeAbbr:
		return t.z + t.dst*3600, t.dst != 0, t.tzAbbr
	case zoneTypeOffset:
		a := t.z
		sign := byte('+')
		if a < 0 {
			sign = '-'
			a = -a
		}
		return t.z, false, fmt.Sprintf("GMT%c%02d%02d", sign, a/3600, (a%3600)/60)
	case zoneTypeID:
		off, _, dst, ab := t.tz.offsetInfo(t.sse)
		return off, dst, ab
	}
	return 0, false, ""
}

var dayFullNames = [...]string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
var monthFullNames = [...]string{"January", "February", "March", "April", "May", "June", "July", "August",
	"September", "October", "November", "December"}

func englishSuffix(d int64) string {
	if d >= 10 && d <= 19 {
		return "th"
	}
	switch d % 10 {
	case 1:
		return "st"
	case 2:
		return "nd"
	case 3:
		return "rd"
	}
	return "th"
}

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// Format is DateTime::format($format), PHP's date() letters included.
func (d *DateTime) Format(format string) string {
	t := &d.t
	off, isDST, abbr := d.offset()
	var b strings.Builder
	weekYearSet := false
	var isoW, isoY int64
	sign := func() byte {
		if off < 0 {
			return '-'
		}
		return '+'
	}
	for i := 0; i < len(format); i++ {
		switch c := format[i]; c {
		case 'd':
			fmt.Fprintf(&b, "%02d", t.d)
		case 'D':
			b.WriteString(dayFullNames[dayOfWeek(t.y, t.m, t.d)][:3])
		case 'j':
			fmt.Fprintf(&b, "%d", t.d)
		case 'l':
			b.WriteString(dayFullNames[dayOfWeek(t.y, t.m, t.d)])
		case 'S':
			b.WriteString(englishSuffix(t.d))
		case 'w':
			fmt.Fprintf(&b, "%d", dayOfWeek(t.y, t.m, t.d))
		case 'N':
			fmt.Fprintf(&b, "%d", isoDayOfWeek(t.y, t.m, t.d))
		case 'z':
			fmt.Fprintf(&b, "%d", dayOfYear(t.y, t.m, t.d))
		case 'W', 'o':
			if !weekYearSet {
				isoW, isoY = isoWeek(t.y, t.m, t.d)
				weekYearSet = true
			}
			if c == 'W' {
				fmt.Fprintf(&b, "%02d", isoW)
			} else {
				fmt.Fprintf(&b, "%d", isoY)
			}
		case 'F':
			b.WriteString(monthFullNames[t.m-1])
		case 'm':
			fmt.Fprintf(&b, "%02d", t.m)
		case 'M':
			b.WriteString(monthFullNames[t.m-1][:3])
		case 'n':
			fmt.Fprintf(&b, "%d", t.m)
		case 't':
			fmt.Fprintf(&b, "%d", daysInMonth(t.y, t.m))
		case 'L':
			fmt.Fprintf(&b, "%d", boolI64(isLeap(t.y)))
		case 'y':
			fmt.Fprintf(&b, "%02d", t.y%100)
		case 'Y':
			if t.y < 0 {
				b.WriteByte('-')
			}
			fmt.Fprintf(&b, "%04d", abs64(t.y))
		case 'x':
			if t.y < 0 {
				b.WriteByte('-')
			} else if t.y >= 10000 {
				b.WriteByte('+')
			}
			fmt.Fprintf(&b, "%04d", abs64(t.y))
		case 'X':
			if t.y < 0 {
				b.WriteByte('-')
			} else {
				b.WriteByte('+')
			}
			fmt.Fprintf(&b, "%04d", abs64(t.y))
		case 'a':
			if t.h >= 12 {
				b.WriteString("pm")
			} else {
				b.WriteString("am")
			}
		case 'A':
			if t.h >= 12 {
				b.WriteString("PM")
			} else {
				b.WriteString("AM")
			}
		case 'B':
			sse := t.sse
			r := (sse - (sse - ((sse % 86400) + 3600))) * 10
			if r < 0 {
				r += 864000
			}
			r = (r / 864) % 1000
			fmt.Fprintf(&b, "%03d", r)
		case 'g':
			h := t.h % 12
			if h == 0 {
				h = 12
			}
			fmt.Fprintf(&b, "%d", h)
		case 'G':
			fmt.Fprintf(&b, "%d", t.h)
		case 'h':
			h := t.h % 12
			if h == 0 {
				h = 12
			}
			fmt.Fprintf(&b, "%02d", h)
		case 'H':
			fmt.Fprintf(&b, "%02d", t.h)
		case 'i':
			fmt.Fprintf(&b, "%02d", t.i)
		case 's':
			fmt.Fprintf(&b, "%02d", t.s)
		case 'u':
			fmt.Fprintf(&b, "%06d", t.us)
		case 'v':
			fmt.Fprintf(&b, "%03d", t.us/1000)
		case 'I':
			fmt.Fprintf(&b, "%d", boolI64(isDST))
		case 'p', 'P', 'O':
			if c == 'p' && (abbr == "UTC" || abbr == "Z" || abbr == "GMT+0000") {
				b.WriteByte('Z')
				break
			}
			b.WriteString(formatOffset(off, c != 'O'))
			_ = sign
		case 'T':
			b.WriteString(abbr)
		case 'e':
			switch t.zoneType {
			case zoneTypeID:
				b.WriteString(t.tz.Name)
			case zoneTypeAbbr:
				b.WriteString(abbr)
			case zoneTypeOffset:
				b.WriteString(formatOffset(off, true))
			}
		case 'Z':
			fmt.Fprintf(&b, "%d", off)
		case 'c':
			fmt.Fprintf(&b, "%04d-%02d-%02dT%02d:%02d:%02d%s", t.y, t.m, t.d, t.h, t.i, t.s, formatOffset(off, true))
		case 'r':
			fmt.Fprintf(&b, "%3s, %02d %3s %04d %02d:%02d:%02d %s", dayFullNames[dayOfWeek(t.y, t.m, t.d)][:3], t.d,
				monthFullNames[t.m-1][:3], t.y, t.h, t.i, t.s, formatOffset(off, false))
		case 'U':
			fmt.Fprintf(&b, "%d", t.sse)
		case '\\':
			if i+1 < len(format) {
				i++
			}
			b.WriteByte(format[i])
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// JSONSerialize gives json_encode's view of a DateTime (its debug properties).
func (d *DateTime) JSONSerialize() any {
	zone := d.Timezone().Name()
	return NewMap("date", d.Format("Y-m-d H:i:s.u"), "timezone_type", int64(d.t.zoneType), "timezone", zone)
}

// isCtypeDigit is ctype_digit on a string.
func isCtypeDigit(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// ConvertDate is CoreExtension::convertDate. zone is the Twig timezone argument: nil for the default zone,
// false to keep the date's own zone, a zone name (string) or a *DateTimeZone. defaultZone is the core
// extension's timezone; now is the current time.
func ConvertDate(date any, zone any, defaultZone *DateTimeZone, now time.Time) (*DateTime, error) {
	var tz *DateTimeZone
	keep := false
	switch z := zone.(type) {
	case nil:
		tz = defaultZone
	case bool:
		if z {
			name, _ := ToString(z)
			t, err := NewDateTimeZone(name)
			if err != nil {
				return nil, err
			}
			tz = t
		} else {
			keep = true
		}
	case *DateTimeZone:
		tz = z
	default:
		name, err := StringArg("DateTimeZone::__construct", 1, "timezone", z)
		if err != nil {
			return nil, err
		}
		t, err := NewDateTimeZone(name)
		if err != nil {
			return nil, err
		}
		tz = t
	}
	if d, ok := date.(*DateTime); ok {
		c := d.Clone()
		if !keep {
			c.SetTimezone(tz)
		}
		return c, nil
	}
	if s, ok := date.(string); date == nil || ok && s == "now" {
		z := tz
		if keep {
			z = defaultZone
		}
		return NewDateTime("now", z, now)
	}
	str, err := ToString(date)
	if err != nil {
		return nil, err
	}
	var d *DateTime
	if isCtypeDigit(str) || len(str) > 1 && str[0] == '-' && isCtypeDigit(str[1:]) {
		d, err = NewDateTime("@"+str, nil, now)
	} else {
		d, err = NewDateTime(str, nil, now)
	}
	if err != nil {
		return nil, err
	}
	if !keep {
		d.SetTimezone(tz)
	}
	return d, nil
}

// DateTimeFromTimestamp returns the DateTime for a Unix timestamp in the given zone.
func DateTimeFromTimestamp(ts int64, zone *DateTimeZone) *DateTime {
	d, _ := NewDateTime("@"+strconv.FormatInt(ts, 10), nil, time.Unix(ts, 0))
	d.SetTimezone(zone)
	return d
}

// FormatDate is $date->format($format).
func FormatDate(d *DateTime, format string) string { return d.Format(format) }

// ModifyDate is CoreExtension::modifyDate: the date converted with its own zone kept, then modified.
func ModifyDate(date any, modifier string, defaultZone *DateTimeZone, now time.Time) (*DateTime, error) {
	d, err := ConvertDate(date, false, defaultZone, now)
	if err != nil {
		return nil, err
	}
	if err := d.Modify(modifier); err != nil {
		return nil, err
	}
	return d, nil
}

// ValidTimezone reports whether new DateTimeZone($name) succeeds.
func ValidTimezone(name string) bool {
	_, err := NewDateTimeZone(name)
	return err == nil
}
