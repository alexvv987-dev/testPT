package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"time"

	"github.com/alexvv987-dev/testPt/internal/shortener"
)

const maxRequestBody = 16 * 1024

type Service interface {
	Shorten(context.Context, string) (shortener.Result, error)
	Resolve(context.Context, string) (string, error)
}

type Pinger interface {
	Ping(context.Context) error
}

type Handler struct {
	service       Service
	pinger        Pinger
	publicBaseURL string
	logger        *slog.Logger
}

type shortenRequest struct {
	URL string `json:"url"`
}

func (r *shortenRequest) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	opening, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := opening.(json.Delim); !ok || delimiter != '{' {
		return errors.New("request must be an object")
	}

	seenURL := false
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok || key != "url" {
			return errors.New("unknown request field")
		}
		if seenURL {
			return errors.New("duplicate url field")
		}

		var value *string
		if err := decoder.Decode(&value); err != nil {
			return err
		}
		if value == nil {
			return errors.New("url must be a string")
		}
		r.URL = *value
		seenURL = true
	}

	closing, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := closing.(json.Delim); !ok || delimiter != '}' || !seenURL {
		return errors.New("url field is required")
	}
	return nil
}

type shortenResponse struct {
	ShortURL string `json:"short_url"`
}

type errorEnvelope struct {
	Error errorResponse `json:"error"`
}

type errorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func New(service Service, pinger Pinger, publicBaseURL string, logger *slog.Logger, postLimiter, readLimiter *ClientLimiter, globalGuard *GlobalGuard) http.Handler {
	handler := &Handler{
		service:       service,
		pinger:        pinger,
		publicBaseURL: publicBaseURL,
		logger:        logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/shorten", handler.shorten)
	mux.HandleFunc("/healthz", handler.health)
	mux.HandleFunc("/{code}", handler.redirect)
	mux.HandleFunc("/", handler.notFound)

	var result http.Handler = mux
	// Wrapping is intentionally inside-out. At runtime cheap per-client checks
	// execute before the shared global budget and concurrency slots are consumed.
	result = globalGuard.Middleware(result)
	result = postLimiter.Middleware(result)
	result = readLimiter.MiddlewareAll(result)
	result = recoverPanics(logger, result)
	result = requestLogging(logger, result)
	result = requestID(result)
	result = securityHeaders(result)
	return result
}

func (h *Handler) shorten(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, http.MethodPost)
		return
	}

	contentType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		writeError(writer, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return
	}

	request.Body = http.MaxBytesReader(writer, request.Body, maxRequestBody)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()

	var payload shortenRequest
	if err := decoder.Decode(&payload); err != nil {
		writeDecodeError(writer, err)
		return
	}
	if err := ensureJSONEnd(decoder); err != nil {
		writeDecodeError(writer, err)
		return
	}

	result, err := h.service.Shorten(request.Context(), payload.URL)
	if err != nil {
		switch {
		case errors.Is(err, shortener.ErrInvalidURL):
			writeError(writer, http.StatusBadRequest, "invalid_url", "URL is invalid or not allowed")
		case errors.Is(err, shortener.ErrStorageUnavailable):
			writeError(writer, http.StatusServiceUnavailable, "service_unavailable", "service is temporarily unavailable")
		case errors.Is(err, shortener.ErrCapacityReached):
			writeError(writer, http.StatusServiceUnavailable, "capacity_reached", "link capacity is temporarily exhausted")
		default:
			writeError(writer, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}

	shortURL := h.publicBaseURL + "/" + result.Code
	status := http.StatusOK
	if result.Created {
		status = http.StatusCreated
	}
	writer.Header().Set("Location", shortURL)
	writeJSON(writer, status, shortenResponse{ShortURL: shortURL})
}

func (h *Handler) redirect(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		methodNotAllowed(writer, http.MethodGet, http.MethodHead)
		return
	}

	originalURL, err := h.service.Resolve(request.Context(), request.PathValue("code"))
	if err != nil {
		switch {
		case errors.Is(err, shortener.ErrInvalidCode), errors.Is(err, shortener.ErrNotFound):
			writeError(writer, http.StatusNotFound, "not_found", "short URL not found")
		case errors.Is(err, shortener.ErrStorageUnavailable):
			writeError(writer, http.StatusServiceUnavailable, "service_unavailable", "service is temporarily unavailable")
		default:
			writeError(writer, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}
	http.Redirect(writer, request, originalURL, http.StatusFound)
}

func (h *Handler) health(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		methodNotAllowed(writer, http.MethodGet, http.MethodHead)
		return
	}

	ctx, cancel := context.WithTimeout(request.Context(), time.Second)
	defer cancel()
	if err := h.pinger.Ping(ctx); err != nil {
		writeError(writer, http.StatusServiceUnavailable, "service_unavailable", "service is not ready")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) notFound(writer http.ResponseWriter, _ *http.Request) {
	writeError(writer, http.StatusNotFound, "not_found", "resource not found")
}

func methodNotAllowed(writer http.ResponseWriter, methods ...string) {
	for _, method := range methods {
		writer.Header().Add("Allow", method)
	}
	writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain one JSON object")
		}
		return err
	}
	return nil
}

func writeDecodeError(writer http.ResponseWriter, err error) {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		writeError(writer, http.StatusRequestEntityTooLarge, "request_too_large", "request body is too large")
		return
	}
	writeError(writer, http.StatusBadRequest, "invalid_request", "request body must be a valid JSON object")
}

func writeError(writer http.ResponseWriter, status int, code, message string) {
	writeJSON(writer, status, errorEnvelope{Error: errorResponse{Code: code, Message: message}})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
