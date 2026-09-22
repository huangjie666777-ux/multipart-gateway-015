package main

import (
	"encoding/json"
	"errors"
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

func (s *server) parseForm(w http.ResponseWriter, req *http.Request) {
	result, err := forms.Parse(req.Header.Get("Content-Type"), req.Body, s.limits)
	if err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, forms.ErrLimitExceeded) {
			code = http.StatusRequestEntityTooLarge
		}
		writeError(w, code, err.Error())
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
