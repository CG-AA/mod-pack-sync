package transport

import "testing"

func TestAttempts(t *testing.T) {
	cases := []struct {
		flag, setting, want int
	}{
		{0, 0, 3},  // neither set -> default
		{5, 0, 5},  // flag wins
		{0, 2, 2},  // setting used when no flag
		{5, 2, 5},  // flag overrides setting
		{-1, 0, 3}, // nonsensical flag treated as unset
	}
	for _, c := range cases {
		if got := Attempts(c.flag, c.setting); got != c.want {
			t.Errorf("Attempts(%d,%d) = %d, want %d", c.flag, c.setting, got, c.want)
		}
	}
}
