package shortener

import (
	"context"
	"errors"
	"fmt"
)

const defaultCollisionAttempts = 10

type SaveResult struct {
	Code          string
	Created       bool
	CodeCollision bool
}

type Repository interface {
	Save(context.Context, string, string) (SaveResult, error)
	FindURL(context.Context, string) (string, error)
}

type Result struct {
	Code    string
	Created bool
}

type Service struct {
	repository Repository
	validator  Validator
	generator  Generator
	attempts   int
}

func NewService(repository Repository, validator Validator, generator Generator) *Service {
	return &Service{
		repository: repository,
		validator:  validator,
		generator:  generator,
		attempts:   defaultCollisionAttempts,
	}
}

func (s *Service) Shorten(ctx context.Context, originalURL string) (Result, error) {
	if err := s.validator.Validate(originalURL); err != nil {
		return Result{}, ErrInvalidURL
	}

	for range s.attempts {
		code, err := s.generator.Generate()
		if err != nil {
			return Result{}, err
		}
		result, err := s.repository.Save(ctx, code, originalURL)
		if err != nil {
			return Result{}, fmt.Errorf("%w: %v", ErrStorageUnavailable, err)
		}
		if result.CodeCollision {
			continue
		}
		return Result{Code: result.Code, Created: result.Created}, nil
	}
	return Result{}, ErrCollisionExhausted
}

func (s *Service) Resolve(ctx context.Context, code string) (string, error) {
	if !ValidCode(code) {
		return "", ErrInvalidCode
	}
	originalURL, err := s.repository.FindURL(ctx, code)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("%w: %v", ErrStorageUnavailable, err)
	}
	return originalURL, nil
}
