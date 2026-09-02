package shortener

import (
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

const MaxURLLength = 2048

type Validator interface {
	Validate(rawURL string) error
}

type URLValidator struct{}

func (URLValidator) Validate(rawURL string) error {
	if rawURL == "" || len(rawURL) > MaxURLLength {
		return ErrInvalidURL
	}
	for _, character := range rawURL {
		if unicode.IsControl(character) {
			return ErrInvalidURL
		}
	}

	parsed, err := url.ParseRequestURI(rawURL)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" {
		return ErrInvalidURL
	}
	if !strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https") {
		return ErrInvalidURL
	}
	if parsed.User != nil {
		return ErrInvalidURL
	}

	hostname := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if hostname == "" || hostname == "localhost" || strings.HasSuffix(hostname, ".localhost") {
		return ErrInvalidURL
	}
	if !isASCII(hostname) {
		return ErrInvalidURL
	}
	if strings.Contains(hostname, "%") {
		return ErrInvalidURL
	}

	if address, err := netip.ParseAddr(hostname); err == nil {
		address = address.Unmap()
		if address.IsLoopback() || address.IsPrivate() || address.IsLinkLocalUnicast() ||
			address.IsLinkLocalMulticast() || address.IsUnspecified() || address.IsMulticast() {
			return ErrInvalidURL
		}
	} else if looksLikeObfuscatedIP(hostname) {
		return ErrInvalidURL
	}

	if parsed.Port() != "" {
		port, err := strconv.Atoi(parsed.Port())
		if err != nil || port < 1 || port > 65535 {
			return ErrInvalidURL
		}
	}
	return nil
}

func isASCII(value string) bool {
	for _, character := range value {
		if character > unicode.MaxASCII {
			return false
		}
	}
	return true
}

func looksLikeObfuscatedIP(hostname string) bool {
	parts := strings.Split(strings.ToLower(hostname), ".")
	for _, part := range parts {
		if part == "" {
			return false
		}
		if strings.HasPrefix(part, "0x") {
			if len(part) == 2 || !allCharacters(part[2:], isHexDigit) {
				return false
			}
			continue
		}
		if !allCharacters(part, isDecimalDigit) {
			return false
		}
	}
	return true
}

func allCharacters(value string, predicate func(rune) bool) bool {
	for _, character := range value {
		if !predicate(character) {
			return false
		}
	}
	return true
}

func isDecimalDigit(character rune) bool {
	return character >= '0' && character <= '9'
}

func isHexDigit(character rune) bool {
	return isDecimalDigit(character) || (character >= 'a' && character <= 'f')
}
