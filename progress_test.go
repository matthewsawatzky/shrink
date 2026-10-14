package main

import "testing"

func TestHumanDuration(t *testing.T) {
	verbose = false
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
		if got := humanDuration(c.in); got != c.want {
			t.Errorf("humanDuration(%v) = %q, want %q", c.in, got, c.want)
		}
	}
	verbose = true
	if got := humanDuration(10); got != "10000ms" {
		t.Errorf("verbose humanDuration(10) = %q, want 10000ms", got)
	}
	verbose = false
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

func TestOutputArgFromArgs(t *testing.T) {
	args := []string{"-i", "in.mp4", "-crf", "23", "out.mp4"}
	if got := outputArgFromArgs(args); got != "out.mp4" {
		t.Errorf("outputArgFromArgs = %q, want out.mp4", got)
	}
	if got := outputArgFromArgs([]string{"-y", "-i"}); got != "" {
		t.Errorf("trailing flag should not be the output, got %q", got)
	}
}

func TestInputPathFromArgs(t *testing.T) {
	args := []string{"-i", "in.mp4", "-crf", "23", "out.mp4"}
	if got := inputPathFromArgs(args); got != "in.mp4" {
		t.Errorf("inputPathFromArgs = %q, want in.mp4", got)
	}
	if got := inputPathFromArgs([]string{"-y", "out.mp4"}); got != "" {
		t.Errorf("no -i flag should give empty input, got %q", got)
	}
}
