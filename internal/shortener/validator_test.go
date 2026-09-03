package shortener

import (
	"errors"
	"strings"
	"testing"
)

func TestURLValidator(t *testing.T) {
	validator := URLValidator{}
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{name: "https", url: "https://example.com/path?q=value#fragment"},
		{name: "uppercase scheme", url: "HTTPS://example.com/path"},
		{name: "http with port", url: "http://example.com:8080/path"},
		{name: "unicode domain", url: "https://пример.рф/путь", wantErr: true},
		{name: "unicode localhost separator", url: "http://localhost。/path", wantErr: true},
		{name: "fullwidth loopback", url: "http://１２７。０。０。１/path", wantErr: true},
		{name: "empty", url: "", wantErr: true},
		{name: "relative", url: "/relative", wantErr: true},
		{name: "missing host", url: "https:///path", wantErr: true},
		{name: "unsupported scheme", url: "ftp://example.com/file", wantErr: true},
		{name: "credentials", url: "https://user:password@example.com", wantErr: true},
		{name: "control character", url: "https://example.com/line\nbreak", wantErr: true},
		{name: "localhost", url: "http://localhost/test", wantErr: true},
		{name: "localhost subdomain", url: "http://api.localhost/test", wantErr: true},
		{name: "private ipv4", url: "http://192.168.1.1/test", wantErr: true},
		{name: "loopback ipv4", url: "http://127.0.0.1/test", wantErr: true},
		{name: "link local ipv4", url: "http://169.254.1.1/test", wantErr: true},
		{name: "private ipv6", url: "http://[fd00::1]/test", wantErr: true},
		{name: "mapped private ipv6", url: "http://[::ffff:127.0.0.1]/test", wantErr: true},
		{name: "unspecified ipv6", url: "http://[::]/test", wantErr: true},
		{name: "decimal ip", url: "http://2130706433/test", wantErr: true},
		{name: "hex ip", url: "http://0x7f000001/test", wantErr: true},
		{name: "dotted octal", url: "http://0177.0.0.1/test", wantErr: true},
		{name: "normal hex-looking domain", url: "https://dead.beef/path"},
		{name: "invalid port", url: "https://example.com:70000/path", wantErr: true},
		{name: "too long", url: "https://example.com/" + strings.Repeat("a", MaxURLLength), wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validator.Validate(test.url)
			if test.wantErr && !errors.Is(err, ErrInvalidURL) {
				t.Fatalf("Validate(%q) error = %v, want ErrInvalidURL", test.url, err)
			}
			if !test.wantErr && err != nil {
				t.Fatalf("Validate(%q) unexpected error = %v", test.url, err)
			}
		})
	}
}

func FuzzURLValidator(f *testing.F) {
	seeds := []string{
		"https://example.com",
		"http://127.0.0.1",
		"javascript:alert(1)",
		"https://user:password@example.com",
		"https://пример.рф/путь",
		"http://localhost。/path",
		"http://１２７。０。０。１/path",
		"\x00https://example.com",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}
	validator := URLValidator{}
	f.Fuzz(func(_ *testing.T, rawURL string) {
		_ = validator.Validate(rawURL)
	})
}
