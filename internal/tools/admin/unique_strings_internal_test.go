package admin

import (
	"reflect"
	"testing"
)

func TestUniqueStringsCollapsesAdjacentDuplicates(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"empty", nil, nil},
		{"single", []string{"a"}, []string{"a"}},
		{"no duplicates", []string{"a", "b", "c"}, []string{"a", "b", "c"}},
		{"adjacent duplicates", []string{"a", "a", "b", "b", "b", "c"}, []string{"a", "b", "c"}},
		{"all same", []string{"x", "x", "x"}, []string{"x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := uniqueStrings(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("uniqueStrings(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
