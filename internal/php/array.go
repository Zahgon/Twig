// This file is part of the Go port of Twig.
//
// (c) Fabien Potencier
//
// For the full copyright and license information, please view the LICENSE
// file that was distributed with this source code.

package php

import (
	"math"
	"strconv"
)

// Array is a PHP array: an ordered map whose keys are int64 or string. Arrays have value semantics in PHP; here
// they are pointers, so code that modifies an array it did not create must Copy it first.
type Array struct {
	entries []entry
	index   map[any]int
	count   int
	next    int64
}

type entry struct {
	key   any
	value any
	live  bool
}

// NewArray returns an empty array.
func NewArray() *Array {
	return &Array{index: map[any]int{}}
}

// NewList builds a list (keys 0..n-1) from values.
func NewList(values ...any) *Array {
	a := &Array{index: make(map[any]int, len(values))}
	for _, v := range values {
		a.Append(Normalize(v))
	}
	return a
}

// NewMap builds an array from alternating key, value arguments.
func NewMap(kv ...any) *Array {
	a := NewArray()
	for i := 0; i+1 < len(kv); i += 2 {
		a.Set(kv[i], Normalize(kv[i+1]))
	}
	return a
}

// NormalizeKey converts a value to an array key as PHP does: decimal integer strings ("12", "-3", but not "012",
// "-0" or "1.5") become ints, bools become 0/1, floats truncate, null becomes "".
func NormalizeKey(k any) any {
	switch k := k.(type) {
	case int64:
		return k
	case int:
		return int64(k)
	case string:
		if n, ok := intKeyString(k); ok {
			return n
		}
		return k
	case bool:
		if k {
			return int64(1)
		}
		return int64(0)
	case float64:
		return FloatToInt(k)
	case nil:
		return ""
	case Markup:
		return NormalizeKey(k.S)
	}
	return k
}

// intKeyString implements ZEND_HANDLE_NUMERIC_STR.
func intKeyString(s string) (int64, bool) {
	if s == "" || len(s) > 20 {
		return 0, false
	}
	i := 0
	if s[0] == '-' {
		i = 1
		if len(s) == 1 {
			return 0, false
		}
	}
	if s[i] == '0' && len(s) > i+1 {
		return 0, false
	}
	if s == "-0" {
		return 0, false
	}
	for j := i; j < len(s); j++ {
		if s[j] < '0' || s[j] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// Len is count($a).
func (a *Array) Len() int {
	if a == nil {
		return 0
	}
	return a.count
}

// Get returns the value at key k (after key normalisation).
func (a *Array) Get(k any) (any, bool) {
	if a == nil {
		return nil, false
	}
	i, ok := a.index[NormalizeKey(k)]
	if !ok {
		return nil, false
	}
	return a.entries[i].value, true
}

// Has reports whether the key exists (array_key_exists).
func (a *Array) Has(k any) bool {
	_, ok := a.Get(k)
	return ok
}

// Set stores v at key k, keeping the position of an existing key.
func (a *Array) Set(k any, v any) {
	k = NormalizeKey(k)
	if i, ok := a.index[k]; ok {
		a.entries[i].value = v
		return
	}
	a.entries = append(a.entries, entry{key: k, value: v, live: true})
	a.index[k] = len(a.entries) - 1
	a.count++
	if n, ok := k.(int64); ok && n >= a.next {
		if n == math.MaxInt64 {
			a.next = n
		} else {
			a.next = n + 1
		}
	}
}

// Append is $a[] = v.
func (a *Array) Append(v any) {
	a.Set(a.next, v)
}

// Delete is unset($a[k]).
func (a *Array) Delete(k any) {
	k = NormalizeKey(k)
	i, ok := a.index[k]
	if !ok {
		return
	}
	delete(a.index, k)
	a.entries[i] = entry{}
	a.count--
	if a.count*2 < len(a.entries) && len(a.entries) > 8 {
		a.compact()
	}
}

func (a *Array) compact() {
	live := make([]entry, 0, a.count)
	for _, e := range a.entries {
		if e.live {
			a.index[e.key] = len(live)
			live = append(live, e)
		}
	}
	a.entries = live
}

// Each calls f for every element in order until f returns false.
func (a *Array) Each(f func(k, v any) bool) {
	if a == nil {
		return
	}
	for _, e := range a.entries {
		if e.live && !f(e.key, e.value) {
			return
		}
	}
}

// Keys returns the keys in order (int64 or string).
func (a *Array) Keys() []any {
	out := make([]any, 0, a.Len())
	a.Each(func(k, _ any) bool { out = append(out, k); return true })
	return out
}

// Values returns the values in order.
func (a *Array) Values() []any {
	out := make([]any, 0, a.Len())
	a.Each(func(_, v any) bool { out = append(out, v); return true })
	return out
}

// Copy returns a shallow copy (nested arrays are shared; copy again before modifying them).
func (a *Array) Copy() *Array {
	b := &Array{index: make(map[any]int, a.Len()), next: 0}
	a.Each(func(k, v any) bool { b.Set(k, v); return true })
	if a != nil {
		b.next = a.next
	}
	return b
}

// IsList is array_is_list: keys are exactly 0, 1, 2, ... in order.
func (a *Array) IsList() bool {
	i := int64(0)
	ok := true
	a.Each(func(k, _ any) bool {
		if n, isInt := k.(int64); !isInt || n != i {
			ok = false
			return false
		}
		i++
		return true
	})
	return ok
}

// First returns the first element.
func (a *Array) First() (key, value any, ok bool) {
	a.Each(func(k, v any) bool { key, value, ok = k, v, true; return false })
	return
}

// Last returns the last element.
func (a *Array) Last() (key, value any, ok bool) {
	if a == nil {
		return
	}
	for i := len(a.entries) - 1; i >= 0; i-- {
		if a.entries[i].live {
			return a.entries[i].key, a.entries[i].value, true
		}
	}
	return
}

// NextIndex is the key the next Append would use.
func (a *Array) NextIndex() int64 { return a.next }
