// This file is part of the Go port of Twig.
//
// (c) Fabien Potencier
//
// For the full copyright and license information, please view the LICENSE
// file that was distributed with this source code.

package php

// ArrayMerge is array_merge: integer keys are renumbered, string keys overwrite.
func ArrayMerge(arrays ...*Array) *Array {
	out := NewArray()
	for _, a := range arrays {
		a.Each(func(k, v any) bool {
			if _, isInt := k.(int64); isInt {
				out.Append(v)
			} else {
				out.Set(k, v)
			}
			return true
		})
	}
	return out
}

// ArraySlice is array_slice($a, $offset, $length, $preserve_keys); hasLength false means a null length.
func ArraySlice(a *Array, offset, length int64, hasLength, preserveKeys bool) *Array {
	n := int64(a.Len())
	out := NewArray()
	if offset > n {
		return out
	}
	if offset < 0 {
		offset += n
		if offset < 0 {
			offset = 0
		}
	}
	if !hasLength {
		length = n
	}
	if length < 0 {
		length = n - offset + length
	} else if length > n-offset {
		length = n - offset
	}
	if length <= 0 {
		return out
	}
	i := int64(0)
	a.Each(func(k, v any) bool {
		if i >= offset+length {
			return false
		}
		if i >= offset {
			if _, isInt := k.(int64); isInt && !preserveKeys {
				out.Append(v)
			} else {
				out.Set(k, v)
			}
		}
		i++
		return true
	})
	return out
}

// ArrayReverse is array_reverse($a, $preserve_keys).
func ArrayReverse(a *Array, preserveKeys bool) *Array {
	keys, vals := a.Keys(), a.Values()
	out := NewArray()
	for i := len(keys) - 1; i >= 0; i-- {
		if _, isInt := keys[i].(int64); isInt && !preserveKeys {
			out.Append(vals[i])
		} else {
			out.Set(keys[i], vals[i])
		}
	}
	return out
}

// ArrayKeys is array_keys.
func ArrayKeys(a *Array) *Array {
	return NewList(a.Keys()...)
}

// ArrayValues is array_values.
func ArrayValues(a *Array) *Array {
	return NewList(a.Values()...)
}

// ArrayChunk is array_chunk($a, $size, $preserve_keys).
func ArrayChunk(a *Array, size int64, preserveKeys bool) (*Array, error) {
	if size < 1 {
		return nil, Throw("ValueError", "array_chunk(): Argument #2 ($length) must be greater than 0")
	}
	out := NewArray()
	var cur *Array
	a.Each(func(k, v any) bool {
		if cur == nil {
			cur = NewArray()
		}
		if preserveKeys {
			cur.Set(k, v)
		} else {
			cur.Append(v)
		}
		if int64(cur.Len()) == size {
			out.Append(cur)
			cur = nil
		}
		return true
	})
	if cur != nil {
		out.Append(cur)
	}
	return out, nil
}

type sortItem struct {
	key, value any
	pos        int
}

// zendSort is zend_sort: insertion sort for up to 16 elements, otherwise a quicksort with median-of-3 (or 5)
// pivots. PHP makes it stable by breaking ties on the original position, but mixed-type comparisons are not a
// total order, so the exact algorithm decides their outcome.
func zendSort(s []sortItem, cmp func(a, b *sortItem) int) {
	for {
		n := len(s)
		if n <= 16 {
			insertSort(s, cmp)
			return
		}
		offset := n >> 1
		pivot := offset
		if n>>10 != 0 {
			delta := offset >> 1
			sort5(s, 0, delta, pivot, pivot+delta, n-1, cmp)
		} else {
			sort3(s, 0, pivot, n-1, cmp)
		}
		s[1], s[pivot] = s[pivot], s[1]
		pivot = 1
		i := pivot + 1
		j := n - 1
	loop:
		for {
			for cmp(&s[pivot], &s[i]) > 0 {
				i++
				if i == j {
					break loop
				}
			}
			j--
			if j == i {
				break
			}
			for cmp(&s[j], &s[pivot]) > 0 {
				j--
				if j == i {
					break loop
				}
			}
			s[i], s[j] = s[j], s[i]
			i++
			if i == j {
				break
			}
		}
		s[pivot], s[i-1] = s[i-1], s[pivot]
		if (i-1)-0 < n-i {
			zendSort(s[:i-1], cmp)
			s = s[i:]
		} else {
			zendSort(s[i:], cmp)
			s = s[:i-1]
		}
	}
}

func sort2(s []sortItem, a, b int, cmp func(a, b *sortItem) int) {
	if cmp(&s[a], &s[b]) > 0 {
		s[a], s[b] = s[b], s[a]
	}
}

func sort3(s []sortItem, a, b, c int, cmp func(a, b *sortItem) int) {
	if !(cmp(&s[a], &s[b]) > 0) {
		if !(cmp(&s[b], &s[c]) > 0) {
			return
		}
		s[b], s[c] = s[c], s[b]
		if cmp(&s[a], &s[b]) > 0 {
			s[a], s[b] = s[b], s[a]
		}
		return
	}
	if !(cmp(&s[c], &s[b]) > 0) {
		s[a], s[c] = s[c], s[a]
		return
	}
	s[a], s[b] = s[b], s[a]
	if cmp(&s[b], &s[c]) > 0 {
		s[b], s[c] = s[c], s[b]
	}
}

func sort4(s []sortItem, a, b, c, d int, cmp func(a, b *sortItem) int) {
	sort3(s, a, b, c, cmp)
	if cmp(&s[c], &s[d]) > 0 {
		s[c], s[d] = s[d], s[c]
		if cmp(&s[b], &s[c]) > 0 {
			s[b], s[c] = s[c], s[b]
			if cmp(&s[a], &s[b]) > 0 {
				s[a], s[b] = s[b], s[a]
			}
		}
	}
}

func sort5(s []sortItem, a, b, c, d, e int, cmp func(a, b *sortItem) int) {
	sort4(s, a, b, c, d, cmp)
	if cmp(&s[d], &s[e]) > 0 {
		s[d], s[e] = s[e], s[d]
		if cmp(&s[c], &s[d]) > 0 {
			s[c], s[d] = s[d], s[c]
			if cmp(&s[b], &s[c]) > 0 {
				s[b], s[c] = s[c], s[b]
				if cmp(&s[a], &s[b]) > 0 {
					s[a], s[b] = s[b], s[a]
				}
			}
		}
	}
}

func insertSort(s []sortItem, cmp func(a, b *sortItem) int) {
	switch len(s) {
	case 0, 1:
		return
	case 2:
		sort2(s, 0, 1, cmp)
		return
	case 3:
		sort3(s, 0, 1, 2, cmp)
		return
	case 4:
		sort4(s, 0, 1, 2, 3, cmp)
		return
	case 5:
		sort5(s, 0, 1, 2, 3, 4, cmp)
		return
	}
	// The first six elements are inserted by a linear scan, the rest by stepping back two at a time.
	sentry := 6
	if sentry > len(s) {
		sentry = len(s)
	}
	for i := 1; i < sentry; i++ {
		j := i - 1
		if !(cmp(&s[j], &s[i]) > 0) {
			continue
		}
		for j != 0 {
			j--
			if !(cmp(&s[j], &s[i]) > 0) {
				j++
				break
			}
		}
		for k := i; k > j; k-- {
			s[k], s[k-1] = s[k-1], s[k]
		}
	}
	for i := sentry; i < len(s); i++ {
		j := i - 1
		if !(cmp(&s[j], &s[i]) > 0) {
			continue
		}
		// Steps back two at a time, as zend_insert_sort does.
		for {
			j -= 2
			if !(cmp(&s[j], &s[i]) > 0) {
				j++
				if !(cmp(&s[j], &s[i]) > 0) {
					j++
				}
				break
			}
			if j == 0 {
				break
			}
			if j == 1 {
				j--
				if cmp(&s[i], &s[j]) > 0 {
					j++
				}
				break
			}
		}
		for k := i; k > j; k-- {
			s[k], s[k-1] = s[k-1], s[k]
		}
	}
}

func sortedArray(a *Array, cmp func(x, y any) (int, error)) (*Array, error) {
	items := make([]sortItem, 0, a.Len())
	a.Each(func(k, v any) bool {
		items = append(items, sortItem{k, v, len(items)})
		return true
	})
	var firstErr error
	zendSort(items, func(x, y *sortItem) int {
		if firstErr != nil {
			return 0
		}
		c, err := cmp(x.value, y.value)
		if err != nil {
			firstErr = err
			return 0
		}
		if c != 0 {
			return c
		}
		// Stable sort: ties keep the original order.
		if x.pos > y.pos {
			return 1
		}
		return -1
	})
	if firstErr != nil {
		return nil, firstErr
	}
	out := NewArray()
	for _, it := range items {
		out.Set(it.key, it.value)
	}
	return out, nil
}

// Asort is asort($a) with SORT_REGULAR: values compared with <=>, keys kept, stable.
func Asort(a *Array) *Array {
	out, _ := sortedArray(a, func(x, y any) (int, error) { return Compare(x, y), nil })
	return out
}

// Uasort is uasort($a, $callback): the callback's result is cast to int and only its sign is used; a callback
// returning false is called again with the arguments swapped (PHP's emulation of boolean comparators).
func Uasort(a *Array, callback func(x, y any) (any, error)) (*Array, error) {
	return sortedArray(a, func(x, y any) (int, error) {
		r, err := callback(x, y)
		if err != nil {
			return 0, err
		}
		if b, ok := r.(bool); ok && !b {
			r, err = callback(y, x)
			if err != nil {
				return 0, err
			}
			return -sign(ToInt(r)), nil
		}
		return sign(ToInt(r)), nil
	})
}

func sign(i int64) int {
	switch {
	case i > 0:
		return 1
	case i < 0:
		return -1
	}
	return 0
}
