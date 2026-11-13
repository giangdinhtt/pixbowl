package id

import (
	"strings"
	"testing"
)

func TestNewIsRandomAndValid(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 500; i++ {
		value, err := New(12)
		if err != nil {
			t.Fatal(err)
		}
		if len(value) != 12 {
			t.Fatalf("length = %d", len(value))
		}
		if !Valid(value) {
			t.Fatalf("%q should be valid", value)
		}
		if seen[value] {
			t.Fatalf("duplicate id %q", value)
		}
		seen[value] = true
	}
}

func TestValidRejectsDangerousInput(t *testing.T) {
	for _, value := range []string{"", "short", "../etc/passwd", "abc/def", "abc def", strings.Repeat("a", 65)} {
		if Valid(value) {
			t.Fatalf("%q should be rejected", value)
		}
	}
}

func TestTokenIsUrlSafeAndUnique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 200; i++ {
		token, err := Token()
		if err != nil {
			t.Fatal(err)
		}
		if strings.ContainsAny(token, "+/=") {
			t.Fatalf("token %q is not URL safe", token)
		}
		if seen[token] {
			t.Fatal("duplicate token")
		}
		seen[token] = true
	}
}

func TestEqual(t *testing.T) {
	if !Equal("abc", "abc") {
		t.Fatal("equal strings should compare equal")
	}
	for _, other := range []string{"abd", "ab", "", "abcd"} {
		if Equal("abc", other) {
			t.Fatalf("%q should not equal abc", other)
		}
	}
}
