package shortener

import (
	"crypto/rand"
	"fmt"
	"io"
)

const (
	// CodeLength is fixed by the public HTTP contract and database constraint.
	CodeLength = 6
	alphabet   = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

type Generator interface {
	Generate() (string, error)
}

type RandomGenerator struct {
	reader io.Reader
}

func NewRandomGenerator() *RandomGenerator {
	return &RandomGenerator{reader: rand.Reader}
}

func NewRandomGeneratorWithReader(reader io.Reader) *RandomGenerator {
	return &RandomGenerator{reader: reader}
}

func (g *RandomGenerator) Generate() (string, error) {
	if g == nil || g.reader == nil {
		return "", fmt.Errorf("%w: random source is unavailable", ErrCodeGeneration)
	}

	result := make([]byte, CodeLength)
	buffer := make([]byte, 1)
	// Reject the incomplete tail of the byte range to avoid modulo bias.
	const acceptanceLimit = byte(256 - (256 % len(alphabet)))

	for index := 0; index < len(result); {
		if _, err := io.ReadFull(g.reader, buffer); err != nil {
			return "", fmt.Errorf("%w: %v", ErrCodeGeneration, err)
		}
		if buffer[0] >= acceptanceLimit {
			continue
		}
		result[index] = alphabet[int(buffer[0])%len(alphabet)]
		index++
	}
	return string(result), nil
}

func ValidCode(code string) bool {
	if len(code) != CodeLength {
		return false
	}
	for index := range len(code) {
		character := code[index]
		if !((character >= '0' && character <= '9') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= 'a' && character <= 'z')) {
			return false
		}
	}
	return true
}
