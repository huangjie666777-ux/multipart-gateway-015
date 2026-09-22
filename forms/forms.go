package forms

import (
	"errors"
	"io"
	"mime"
	"strings"
)

var (
	ErrInvalidContentType = errors.New("invalid multipart content type")
	ErrLimitExceeded      = errors.New("multipart limit exceeded")
	ErrMalformed          = errors.New("malformed multipart body")
	ErrInvalidFilename    = errors.New("invalid filename")
	ErrNotImplemented     = errors.New("multipart parser not implemented")
)

type Limits struct {
	MaxBytes      int64
	MaxParts      int
	MaxFieldBytes int64
	MaxFileBytes  int64
}

func DefaultLimits() Limits {
	return Limits{MaxBytes: 4 << 20, MaxParts: 64, MaxFieldBytes: 1 << 20, MaxFileBytes: 3 << 20}
}

type Part struct {
	Name     string `json:"name"`
	Filename string `json:"filename,omitempty"`
	Value    string `json:"value,omitempty"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256,omitempty"`
}

type Result struct {
	Parts []Part `json:"parts"`
}

func Parse(contentType string, body io.Reader, limits Limits) (Result, error) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.EqualFold(mediaType, "multipart/form-data") || params["boundary"] == "" {
		return Result{}, ErrInvalidContentType
	}
	return Result{}, ErrNotImplemented
}
