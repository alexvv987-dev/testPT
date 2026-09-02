package shortener

import (
	"context"
	"errors"
	"sync"
	"testing"
)

type fakeValidator struct{ err error }

func (v fakeValidator) Validate(string) error { return v.err }

type sequenceGenerator struct {
	mu    sync.Mutex
	codes []string
	err   error
	index int
}

func (g *sequenceGenerator) Generate() (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.err != nil {
		return "", g.err
	}
	code := g.codes[g.index%len(g.codes)]
	g.index++
	return code, nil
}

type memoryRepository struct {
	mu       sync.Mutex
	byCode   map[string]string
	byURL    map[string]string
	saveErr  error
	findErr  error
	forceHit int
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{byCode: make(map[string]string), byURL: make(map[string]string)}
}

func (r *memoryRepository) Save(_ context.Context, code, originalURL string) (SaveResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.saveErr != nil {
		return SaveResult{}, r.saveErr
	}
	if r.forceHit > 0 {
		r.forceHit--
		return SaveResult{CodeCollision: true}, nil
	}
	if existing, ok := r.byURL[originalURL]; ok {
		return SaveResult{Code: existing}, nil
	}
	if _, ok := r.byCode[code]; ok {
		return SaveResult{CodeCollision: true}, nil
	}
	r.byCode[code] = originalURL
	r.byURL[originalURL] = code
	return SaveResult{Code: code, Created: true}, nil
}

func (r *memoryRepository) FindURL(_ context.Context, code string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.findErr != nil {
		return "", r.findErr
	}
	value, ok := r.byCode[code]
	if !ok {
		return "", ErrNotFound
	}
	return value, nil
}

func TestServiceShortenAndResolve(t *testing.T) {
	repository := newMemoryRepository()
	service := NewService(repository, fakeValidator{}, &sequenceGenerator{codes: []string{"abc123", "def456"}})

	first, err := service.Shorten(context.Background(), "https://example.com")
	if err != nil || !first.Created || first.Code != "abc123" {
		t.Fatalf("first Shorten() = %+v, %v", first, err)
	}
	second, err := service.Shorten(context.Background(), "https://example.com")
	if err != nil || second.Created || second.Code != first.Code {
		t.Fatalf("second Shorten() = %+v, %v", second, err)
	}
	resolved, err := service.Resolve(context.Background(), first.Code)
	if err != nil || resolved != "https://example.com" {
		t.Fatalf("Resolve() = %q, %v", resolved, err)
	}
}

func TestServiceRetriesCodeCollision(t *testing.T) {
	repository := newMemoryRepository()
	repository.forceHit = 1
	service := NewService(repository, fakeValidator{}, &sequenceGenerator{codes: []string{"abc123", "def456"}})

	result, err := service.Shorten(context.Background(), "https://example.com")
	if err != nil || result.Code != "def456" || !result.Created {
		t.Fatalf("Shorten() = %+v, %v", result, err)
	}
}

func TestServiceConcurrentDuplicate(t *testing.T) {
	repository := newMemoryRepository()
	service := NewService(repository, fakeValidator{}, &sequenceGenerator{codes: []string{"abc123", "def456", "ghi789"}})

	const workers = 32
	results := make(chan Result, workers)
	errorsChannel := make(chan error, workers)
	var waitGroup sync.WaitGroup
	for range workers {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			result, err := service.Shorten(context.Background(), "https://example.com")
			results <- result
			errorsChannel <- err
		}()
	}
	waitGroup.Wait()
	close(results)
	close(errorsChannel)

	createdCount := 0
	storedCode := ""
	for err := range errorsChannel {
		if err != nil {
			t.Fatalf("Shorten() error = %v", err)
		}
	}
	for result := range results {
		if storedCode == "" {
			storedCode = result.Code
		}
		if result.Code != storedCode {
			t.Errorf("Shorten() code = %q, want %q", result.Code, storedCode)
		}
		if result.Created {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("created count = %d, want 1", createdCount)
	}
}

func TestServiceErrors(t *testing.T) {
	tests := []struct {
		name    string
		service *Service
		call    func(*Service) error
		want    error
	}{
		{
			name:    "invalid url",
			service: NewService(newMemoryRepository(), fakeValidator{err: ErrInvalidURL}, &sequenceGenerator{codes: []string{"abc123"}}),
			call: func(service *Service) error {
				_, err := service.Shorten(context.Background(), "bad")
				return err
			},
			want: ErrInvalidURL,
		},
		{
			name:    "generator",
			service: NewService(newMemoryRepository(), fakeValidator{}, &sequenceGenerator{err: ErrCodeGeneration}),
			call: func(service *Service) error {
				_, err := service.Shorten(context.Background(), "https://example.com")
				return err
			},
			want: ErrCodeGeneration,
		},
		{
			name: "storage save",
			service: NewService(&memoryRepository{saveErr: errors.New("down")}, fakeValidator{},
				&sequenceGenerator{codes: []string{"abc123"}}),
			call: func(service *Service) error {
				_, err := service.Shorten(context.Background(), "https://example.com")
				return err
			},
			want: ErrStorageUnavailable,
		},
		{
			name: "storage find",
			service: NewService(&memoryRepository{findErr: errors.New("down")}, fakeValidator{},
				&sequenceGenerator{codes: []string{"abc123"}}),
			call: func(service *Service) error {
				_, err := service.Resolve(context.Background(), "abc123")
				return err
			},
			want: ErrStorageUnavailable,
		},
		{
			name:    "invalid code",
			service: NewService(newMemoryRepository(), fakeValidator{}, &sequenceGenerator{codes: []string{"abc123"}}),
			call: func(service *Service) error {
				_, err := service.Resolve(context.Background(), "bad")
				return err
			},
			want: ErrInvalidCode,
		},
		{
			name:    "not found",
			service: NewService(newMemoryRepository(), fakeValidator{}, &sequenceGenerator{codes: []string{"abc123"}}),
			call: func(service *Service) error {
				_, err := service.Resolve(context.Background(), "abc123")
				return err
			},
			want: ErrNotFound,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.call(test.service); !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestServiceCollisionExhausted(t *testing.T) {
	repository := newMemoryRepository()
	repository.forceHit = defaultCollisionAttempts
	service := NewService(repository, fakeValidator{}, &sequenceGenerator{codes: []string{"abc123"}})
	_, err := service.Shorten(context.Background(), "https://example.com")
	if !errors.Is(err, ErrCollisionExhausted) {
		t.Fatalf("Shorten() error = %v, want ErrCollisionExhausted", err)
	}
}
