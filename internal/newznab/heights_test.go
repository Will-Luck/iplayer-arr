package newznab

import (
	"reflect"
	"testing"
)

func TestHeightsToTags(t *testing.T) {
	cases := []struct {
		name string
		in   []int
		want []string
	}{
		{"1080p only", []int{1080}, []string{"1080p"}},
		{"720 and 540", []int{720, 540}, []string{"720p", "540p"}},
		{"full ladder", []int{1080, 720, 540, 396}, []string{"1080p", "720p", "540p", "396p"}},
		{"empty", []int{}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := heightsToTags(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("heightsToTags(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
