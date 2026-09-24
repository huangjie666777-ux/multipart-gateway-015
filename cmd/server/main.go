package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/huangjie666777-ux/multipart-gateway-015/forms"
)

type server struct {
	mu      sync.RWMutex
	results map[string]forms.Result
	limits  forms.Limits
}

func main() {
	s := &server{results: make(map[string]forms.Result), limits: forms.DefaultLimits()}
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Post("/v1/forms/{form}", s.parseForm)
	r.Get("/v1/forms/{form}/latest", s.latest)
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":18115"
	}
	if err := http.ListenAndServe(addr, r); err != nil {
		panic(err)
	}
}

// ctxReader stops reads promptly when the client goes away, so parsing and
// hashing abort instead of burning CPU on a cancelled request.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	select {
	case <-c.ctx.Done():
		return 0, c.ctx.Err()
	default:
	}
	return c.r.Read(p)
}

func (s *server) parseForm(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	body := &ctxReader{ctx: ctx, r: req.Body}
	result, err := forms.Parse(req.Header.Get("Content-Type"), body, s.limits)
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			// Client is gone; keep the previous result and do not publish.
			return
		}
		code := http.StatusBadRequest
		if errors.Is(err, forms.ErrLimitExceeded) {
			code = http.StatusRequestEntityTooLarge
		}
		writeError(w, code, err.Error())
		return
	}
	if ctx.Err() != nil {
		// Cancelled after parsing but before publishing: keep old result.
		return
	}
	key := chi.URLParam(req, "form")
	s.mu.Lock()
	s.results[key] = result
	s.mu.Unlock()
	writeJSON(w, http.StatusCreated, result)
}

func (s *server) latest(w http.ResponseWriter, req *http.Request) {
	s.mu.RLock()
	result, ok := s.results[chi.URLParam(req, "form")]
	s.mu.RUnlock()
	if !ok {
		writeError(w, http.StatusNotFound, "form not found")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func writeJSON(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, code int, message string) {
	writeJSON(w, code, map[string]string{"error": message})
}
