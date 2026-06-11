package utils

import (
	"errors"
	"testing"
	"time"
)

func TestTruncateString(t *testing.T) {
	tests := []struct {
		in     string
		maxLen int
		want   string
	}{
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"this is a longer string", 10, "this is..."},
		{"abcdef", 3, "abc"},
		{"abcdef", 2, "ab"},
		{"", 5, ""},
	}
	for _, tt := range tests {
		if got := TruncateString(tt.in, tt.maxLen); got != tt.want {
			t.Errorf("TruncateString(%q, %d) = %q, want %q", tt.in, tt.maxLen, got, tt.want)
		}
	}
}

func TestValidateRequired(t *testing.T) {
	if err := ValidateRequired("value", "field"); err != nil {
		t.Errorf("unexpected error for non-empty value: %v", err)
	}
	if err := ValidateRequired("", "field"); err == nil {
		t.Error("expected error for empty value")
	}
	if err := ValidateRequired("   ", "field"); err == nil {
		t.Error("expected error for whitespace-only value")
	}
}

func TestValidatePositiveDuration(t *testing.T) {
	if err := ValidatePositiveDuration(time.Second, "field"); err != nil {
		t.Errorf("unexpected error for positive duration: %v", err)
	}
	if err := ValidatePositiveDuration(0, "field"); err == nil {
		t.Error("expected error for zero duration")
	}
	if err := ValidatePositiveDuration(-time.Second, "field"); err == nil {
		t.Error("expected error for negative duration")
	}
}

func TestValidateURL(t *testing.T) {
	valid := []string{
		"nats://localhost:4222",
		"tls://nats.example.com:4222",
		"http://localhost:8080",
		"https://example.com",
	}
	for _, url := range valid {
		if err := ValidateURL(url, "field"); err != nil {
			t.Errorf("unexpected error for %q: %v", url, err)
		}
	}

	invalid := []string{"", "localhost:4222", "ftp://example.com"}
	for _, url := range invalid {
		if err := ValidateURL(url, "field"); err == nil {
			t.Errorf("expected error for %q", url)
		}
	}
}

func TestWrapError(t *testing.T) {
	if WrapError(nil, "context") != nil {
		t.Error("WrapError(nil) should return nil")
	}
	if WrapErrorf(nil, "context %s", "x") != nil {
		t.Error("WrapErrorf(nil) should return nil")
	}

	base := errors.New("base error")
	wrapped := WrapError(base, "context")
	if !errors.Is(wrapped, base) {
		t.Error("wrapped error does not unwrap to base error")
	}
	if wrapped.Error() != "context: base error" {
		t.Errorf("wrapped message = %q", wrapped.Error())
	}

	wrappedF := WrapErrorf(base, "op %s failed", "save")
	if !errors.Is(wrappedF, base) {
		t.Error("WrapErrorf result does not unwrap to base error")
	}
	if wrappedF.Error() != "op save failed: base error" {
		t.Errorf("WrapErrorf message = %q", wrappedF.Error())
	}
}
