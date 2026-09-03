package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alexvv987-dev/testPt/internal/shortener"
)

type stubService struct {
	shortenResult shortener.Result
	shortenErr    error
	resolveURL    string
	resolveErr    error
}

func (s *stubService) Shorten(context.Context, string) (shortener.Result, error) {
	return s.shortenResult, s.shortenErr
}

func (s *stubService) Resolve(context.Context, string) (string, error) {
	return s.resolveURL, s.resolveErr
}

type stubPinger struct{ err error }

func (p stubPinger) Ping(context.Context) error { return p.err }

func testHandler(t *testing.T, service *stubService, pinger stubPinger, logOutput io.Writer) http.Handler {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(logOutput, nil))
	postLimiter := NewClientLimiter(10_000, 1_000)
	readLimiter := NewClientLimiter(10_000, 1_000)
	t.Cleanup(postLimiter.Close)
	t.Cleanup(readLimiter.Close)
	return New(service, pinger, "http://localhost:8080", logger, postLimiter, readLimiter, NewGlobalGuard(10_000, 1_000, 1_000))
}

func performRequest(handler http.Handler, method, target, body, contentType string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.RemoteAddr = "192.0.2.1:12345"
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestShortenHandler(t *testing.T) {
	tests := []struct {
		name       string
		service    *stubService
		body       string
		content    string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "created",
			service:    &stubService{shortenResult: shortener.Result{Code: "abc123", Created: true}},
			body:       `{"url":"https://example.com"}`,
			content:    "application/json; charset=utf-8",
			wantStatus: http.StatusCreated,
		},
		{
			name:       "existing",
			service:    &stubService{shortenResult: shortener.Result{Code: "abc123"}},
			body:       `{"url":"https://example.com"}`,
			content:    "application/json",
			wantStatus: http.StatusOK,
		},
		{
			name:       "unsupported content type",
			service:    &stubService{},
			body:       `{"url":"https://example.com"}`,
			content:    "text/plain",
			wantStatus: http.StatusUnsupportedMediaType,
			wantCode:   "unsupported_media_type",
		},
		{
			name:       "missing content type",
			service:    &stubService{},
			body:       `{"url":"https://example.com"}`,
			wantStatus: http.StatusUnsupportedMediaType,
			wantCode:   "unsupported_media_type",
		},
		{
			name:       "malformed json",
			service:    &stubService{},
			body:       `{"url":`,
			content:    "application/json",
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_request",
		},
		{
			name:       "unknown field",
			service:    &stubService{},
			body:       `{"url":"https://example.com","extra":true}`,
			content:    "application/json",
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_request",
		},
		{
			name:       "null payload",
			service:    &stubService{},
			body:       `null`,
			content:    "application/json",
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_request",
		},
		{
			name:       "case insensitive field",
			service:    &stubService{},
			body:       `{"URL":"https://example.com"}`,
			content:    "application/json",
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_request",
		},
		{
			name:       "duplicate field",
			service:    &stubService{},
			body:       `{"url":"https://example.com","url":"https://example.org"}`,
			content:    "application/json",
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_request",
		},
		{
			name:       "trailing object",
			service:    &stubService{},
			body:       `{"url":"https://example.com"}{}`,
			content:    "application/json",
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_request",
		},
		{
			name:       "invalid url",
			service:    &stubService{shortenErr: shortener.ErrInvalidURL},
			body:       `{"url":"bad"}`,
			content:    "application/json",
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_url",
		},
		{
			name:       "database unavailable",
			service:    &stubService{shortenErr: shortener.ErrStorageUnavailable},
			body:       `{"url":"https://example.com"}`,
			content:    "application/json",
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "service_unavailable",
		},
		{
			name:       "internal failure",
			service:    &stubService{shortenErr: shortener.ErrCollisionExhausted},
			body:       `{"url":"https://example.com"}`,
			content:    "application/json",
			wantStatus: http.StatusInternalServerError,
			wantCode:   "internal_error",
		},
		{
			name:       "capacity reached",
			service:    &stubService{shortenErr: shortener.ErrCapacityReached},
			body:       `{"url":"https://example.com"}`,
			content:    "application/json",
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "capacity_reached",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := testHandler(t, test.service, stubPinger{}, io.Discard)
			recorder := performRequest(handler, http.MethodPost, "/shorten", test.body, test.content)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
			if recorder.Header().Get("Content-Type") != "application/json; charset=utf-8" {
				t.Fatalf("Content-Type = %q", recorder.Header().Get("Content-Type"))
			}
			if recorder.Header().Get("X-Request-ID") == "" {
				t.Fatal("X-Request-ID is empty")
			}
			if test.wantStatus == http.StatusCreated || test.wantStatus == http.StatusOK {
				var response shortenResponse
				if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
					t.Fatalf("decode response: %v", err)
				}
				if response.ShortURL != "http://localhost:8080/abc123" {
					t.Fatalf("short_url = %q", response.ShortURL)
				}
				if recorder.Header().Get("Location") != response.ShortURL {
					t.Fatalf("Location = %q", recorder.Header().Get("Location"))
				}
			}
			if test.wantCode != "" {
				assertErrorCode(t, recorder, test.wantCode)
			}
		})
	}
}

func TestShortenHandlerRejectsOversizedBody(t *testing.T) {
	handler := testHandler(t, &stubService{}, stubPinger{}, io.Discard)
	body := `{"url":"https://example.com/` + strings.Repeat("a", maxRequestBody) + `"}`
	recorder := performRequest(handler, http.MethodPost, "/shorten", body, "application/json")
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusRequestEntityTooLarge)
	}
	assertErrorCode(t, recorder, "request_too_large")
}

func TestRedirectHandler(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		service    *stubService
		wantStatus int
		wantCode   string
	}{
		{name: "get", method: http.MethodGet, service: &stubService{resolveURL: "https://example.com/path"}, wantStatus: http.StatusFound},
		{name: "head", method: http.MethodHead, service: &stubService{resolveURL: "https://example.com/path"}, wantStatus: http.StatusFound},
		{name: "not found", method: http.MethodGet, service: &stubService{resolveErr: shortener.ErrNotFound}, wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{name: "invalid code", method: http.MethodGet, service: &stubService{resolveErr: shortener.ErrInvalidCode}, wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{name: "database unavailable", method: http.MethodGet, service: &stubService{resolveErr: shortener.ErrStorageUnavailable}, wantStatus: http.StatusServiceUnavailable, wantCode: "service_unavailable"},
		{name: "internal", method: http.MethodGet, service: &stubService{resolveErr: errors.New("unexpected")}, wantStatus: http.StatusInternalServerError, wantCode: "internal_error"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := testHandler(t, test.service, stubPinger{}, io.Discard)
			recorder := performRequest(handler, test.method, "/abc123", "", "")
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, test.wantStatus)
			}
			if test.wantStatus == http.StatusFound && recorder.Header().Get("Location") != "https://example.com/path" {
				t.Fatalf("Location = %q", recorder.Header().Get("Location"))
			}
			if recorder.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("Cache-Control = %q", recorder.Header().Get("Cache-Control"))
			}
			if test.wantCode != "" {
				assertErrorCode(t, recorder, test.wantCode)
			}
		})
	}
}

func TestHealthAndRouting(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		target     string
		pinger     stubPinger
		wantStatus int
		wantCode   string
	}{
		{name: "healthy", method: http.MethodGet, target: "/healthz", wantStatus: http.StatusOK},
		{name: "unhealthy", method: http.MethodGet, target: "/healthz", pinger: stubPinger{err: errors.New("down")}, wantStatus: http.StatusServiceUnavailable, wantCode: "service_unavailable"},
		{name: "shorten method", method: http.MethodGet, target: "/shorten", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed"},
		{name: "redirect method", method: http.MethodDelete, target: "/abc123", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed"},
		{name: "unknown path", method: http.MethodGet, target: "/path/extra", wantStatus: http.StatusNotFound, wantCode: "not_found"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := testHandler(t, &stubService{}, test.pinger, io.Discard)
			recorder := performRequest(handler, test.method, test.target, "", "")
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, test.wantStatus)
			}
			if test.wantCode != "" {
				assertErrorCode(t, recorder, test.wantCode)
			}
		})
	}
}

func TestLogsDoNotContainOriginalURL(t *testing.T) {
	var logs bytes.Buffer
	handler := testHandler(t, &stubService{shortenResult: shortener.Result{Code: "abc123", Created: true}}, stubPinger{}, &logs)
	sensitiveURL := "https://example.com/path?token=top-secret"
	recorder := performRequest(handler, http.MethodPost, "/shorten", `{"url":"`+sensitiveURL+`"}`, "application/json")
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d", recorder.Code)
	}
	if strings.Contains(logs.String(), sensitiveURL) || strings.Contains(logs.String(), "top-secret") {
		t.Fatalf("logs contain sensitive URL: %s", logs.String())
	}
}

func assertErrorCode(t *testing.T, recorder *httptest.ResponseRecorder, want string) {
	t.Helper()
	var response errorEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode error response: %v; body=%s", err, recorder.Body.String())
	}
	if response.Error.Code != want {
		t.Fatalf("error code = %q, want %q", response.Error.Code, want)
	}
}
