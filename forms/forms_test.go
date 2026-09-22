package forms

import (
	"strings"
	"testing"
)

func TestParseRejectsNonMultipart(t *testing.T) {
	_, err := Parse("application/json", strings.NewReader("{}"), DefaultLimits())
	if err != ErrInvalidContentType {
		t.Fatalf("got %v", err)
	}
}

func TestDefaultLimitsAreBounded(t *testing.T) {
	l := DefaultLimits()
	if l.MaxBytes <= l.MaxFileBytes || l.MaxParts < 2 {
		t.Fatalf("unexpected limits: %+v", l)
	}
}
