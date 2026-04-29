package util

import (
	"cmp"
	"iter"
	"maps"
	"slices"
)

// SortedByKeys iterates over a given m sorted by the map keys.
// This is helpful when Go's random map iteration must be avoided.
func SortedByKeys[K cmp.Ordered, V any](m map[K]V) iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		for _, k := range slices.Sorted(maps.Keys(m)) {
			if !yield(k, m[k]) {
				return
			}
		}
	}
}
