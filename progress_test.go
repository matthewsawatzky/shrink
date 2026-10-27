package main

import "testing"

func TestHumanDuration(t *testing.T) {
	a := testApplication()

	a.verbose = false
	cases := []struct {
		in   float64
		want string
	}{
		{0.42, "420ms"},
		{4.9, "4900ms"}, // sub-5s keeps ms resolution
		{42, "42s"},
		{192, "3m12s"},
		{3723, "1h02m03s"},
	}
	for _, c := range cases {
		if got := a.humanDuration(c.in); got != c.want {
			t.Errorf("humanDuration(%v) = %q, want %q", c.in, got, c.want)
		}
	}
	a.verbose = true
	if got := a.humanDuration(10); got != "10000ms" {
		t.Errorf("verbose humanDuration(10) = %q, want 10000ms", got)
	}
	a.verbose = false
}

func TestAtof(t *testing.T) {
	if v, ok := atof("12.5"); !ok || v != 12.5 {
		t.Errorf("atof(12.5) = %v, %v", v, ok)
	}
	if _, ok := atof("N/A"); ok {
		t.Error("atof(N/A) should fail")
	}
	if _, ok := atof("junk"); ok {
		t.Error("atof(junk) should fail")
	}
}
