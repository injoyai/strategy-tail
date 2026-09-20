package main

import "testing"

func TestCanonicalCode(t *testing.T) {
	cases := map[string]string{
		"sh600519":  "sh600519",
		"600519.SH": "sh600519",
		"000001":    "sz000001",
		"830799":    "bj830799",
	}
	for input, want := range cases {
		got, err := canonicalCode(input)
		if err != nil || got != want {
			t.Fatalf("canonicalCode(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	if _, err := canonicalCode("bad"); err == nil {
		t.Fatal("invalid code should fail")
	}
}
