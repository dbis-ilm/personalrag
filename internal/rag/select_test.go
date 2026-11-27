package rag

import (
	"slices"
	"testing"
)

func TestSelectSources(t *testing.T) {
	cases := []struct {
		name     string
		scores   []int
		sizes    []int
		budget   int
		used     []int
		relevant int
		fallback bool
	}{
		// The reported case: all 8 relevant must all be used (was capped at 5).
		{"all relevant used", []int{9, 8, 8, 7, 7, 6, 6, 5}, []int{3000, 3000, 3000, 3000, 3000, 3000, 3000, 3000}, contextBudget,
			[]int{0, 1, 2, 3, 4, 5, 6, 7}, 8, false},
		{"irrelevant left out", []int{2, 9, 4, 7}, []int{100, 100, 100, 100}, contextBudget, []int{1, 3}, 2, false},
		{"budget: skip a long one, keep a shorter later one", []int{9, 8, 7}, []int{600, 600, 300}, 1000, []int{0, 2}, 3, false},
		{"best always used even if over budget", []int{9, 8}, []int{5000, 100}, 1000, []int{0}, 2, false},
		{"fallback to best three", []int{1, 4, 3, 2}, []int{10, 10, 10, 10}, contextBudget, []int{1, 2, 3}, 0, true},
	}
	for _, c := range cases {
		used, relevant, fallback := selectSources(c.scores, c.sizes, c.budget)
		if !slices.Equal(used, c.used) || relevant != c.relevant || fallback != c.fallback {
			t.Errorf("%s: got %v/%d/%v, want %v/%d/%v", c.name, used, relevant, fallback, c.used, c.relevant, c.fallback)
		}
	}
}
