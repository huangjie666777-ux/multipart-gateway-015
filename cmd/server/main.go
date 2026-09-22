package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/huangjie666777-ux/schema-registry-015/registry"
)

type registerRequest struct {
	Schema           json.RawMessage `json:"schema"`
	ExpectedRevision *uint64         `json:"expectedRevision,omitempty"`
}

func main() {
	store := registry.NewStore()
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	r.Put("/v1/tenants/{tenant}/subjects/{subject}/versions", func(w http.ResponseWriter, req *http.Request) {
		var in registerRequest
		if json.NewDecoder(req.Body).Decode(&in) != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		v, err := store.Register(chi.URLParam(req, "tenant"), chi.URLParam(req, "subject"), in.Schema, in.ExpectedRevision)
		if err != nil {
			code := http.StatusBadRequest
			if errors.Is(err, registry.ErrConflict) {
				code = http.StatusConflict
			}
			writeError(w, code, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, v)
	})
	r.Get("/v1/tenants/{tenant}/subjects/{subject}/versions/latest", func(w http.ResponseWriter, req *http.Request) {
		v, err := store.Latest(chi.URLParam(req, "tenant"), chi.URLParam(req, "subject"))
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, v)
	})
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":18116"
	}
	if err := http.ListenAndServe(addr, r); err != nil {
		panic(err)
	}
}
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
