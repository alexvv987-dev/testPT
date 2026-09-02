package shortener

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestRandomGeneratorGenerate(t *testing.T) {
	generator := NewRandomGeneratorWithReader(bytes.NewReader([]byte{0, 1, 2, 61, 62, 63}))
	code, err := generator.Generate()
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if code != "012z01" {
		t.Fatalf("Generate() = %q, want %q", code, "012z01")
	}
	if !ValidCode(code) {
		t.Fatalf("generated code %q is invalid", code)
	}
}

func TestRandomGeneratorRejectsBiasedBytes(t *testing.T) {
	generator := NewRandomGeneratorWithReader(bytes.NewReader([]byte{255, 248, 0, 1, 2, 3, 4, 5}))
	code, err := generator.Generate()
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if code != "012345" {
		t.Fatalf("Generate() = %q, want %q", code, "012345")
	}
}

func TestRandomGeneratorFailure(t *testing.T) {
	generator := NewRandomGeneratorWithReader(strings.NewReader(""))
	_, err := generator.Generate()
	if !errors.Is(err, ErrCodeGeneration) {
		t.Fatalf("Generate() error = %v, want ErrCodeGeneration", err)
	}
}

func TestValidCode(t *testing.T) {
	tests := map[string]bool{
		"abc123":  true,
		"ABCxyz":  true,
		"short":   false,
		"toolong": false,
		"abc-12":  false,
		"":        false,
	}
	for code, want := range tests {
		if got := ValidCode(code); got != want {
			t.Errorf("ValidCode(%q) = %v, want %v", code, got, want)
		}
	}
}
