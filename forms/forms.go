package forms

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

var (
	ErrInvalidContentType = errors.New("invalid multipart content type")
	ErrLimitExceeded      = errors.New("multipart limit exceeded")
	ErrMalformed          = errors.New("malformed multipart body")
	ErrInvalidFilename    = errors.New("invalid filename")
)

// Limits caps total wire bytes, part count, and the size of an individual
// text field or file part.
type Limits struct {
	MaxBytes      int64
	MaxParts      int
	MaxFieldBytes int64
	MaxFileBytes  int64
}

func DefaultLimits() Limits {
	return Limits{MaxBytes: 4 << 20, MaxParts: 64, MaxFieldBytes: 1 << 20, MaxFileBytes: 3 << 20}
}

// Part is one ordered entry from a multipart form. Text fields carry Value;
// files carry only metadata and a SHA-256 digest of the full file body.
type Part struct {
	Name     string `json:"name"`
	Filename string `json:"filename,omitempty"`
	Value    string `json:"value,omitempty"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256,omitempty"`
}

// Result is an immutable ordered summary of a successfully parsed form.
type Result struct {
	Parts []Part `json:"parts"`
}

// Parse streams a multipart/form-data body. Any error means no Result is
// produced, so callers never observe partial state. If body returns a context
// cancellation error (e.g. the client disconnects), it is propagated
// unwrapped, letting callers distinguish cancellation from malformed input.
func Parse(contentType string, body io.Reader, limits Limits) (Result, error) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.EqualFold(mediaType, "multipart/form-data") {
		return Result{}, ErrInvalidContentType
	}
	boundary := params["boundary"]
	if boundary == "" || !validBoundary(boundary) {
		return Result{}, ErrInvalidContentType
	}
	if limits.MaxBytes <= 0 || limits.MaxParts <= 0 || limits.MaxFieldBytes < 0 || limits.MaxFileBytes < 0 {
		return Result{}, fmt.Errorf("%w: invalid parser limits", ErrLimitExceeded)
	}
	p := &parser{
		src:      &countingReader{r: body, max: limits.MaxBytes},
		boundary: boundary,
		delim:    []byte("--" + boundary),
		sep:      []byte("\r\n--" + boundary),
		limits:   limits,
		parts:    make([]Part, 0),
	}
	p.r = bufio.NewReaderSize(p.src, 64<<10)
	if err := p.consumeFirstBoundary(); err != nil {
		return Result{}, err
	}
	for {
		closing, err := p.consumeBoundarySuffix()
		if err != nil {
			return Result{}, err
		}
		if closing {
			if b, err := p.readByte(); err != io.EOF {
				if err == nil {
					// Any trailing byte, including a duplicated closing
					// boundary, makes the body malformed.
					_ = b
					err = ErrMalformed
				}
				return Result{}, err
			}
			return Result{Parts: p.parts}, nil
		}
		if len(p.parts) >= limits.MaxParts {
			return Result{}, fmt.Errorf("%w: too many parts", ErrLimitExceeded)
		}
		headers, err := p.readHeaders()
		if err != nil {
			return Result{}, err
		}
		part, isFile, err := buildPart(headers)
		if err != nil {
			return Result{}, err
		}
		if isFile {
			size, digest, err := p.streamFile()
			if err != nil {
				return Result{}, err
			}
			part.Size = size
			part.SHA256 = digest
		} else {
			value, size, err := p.readField()
			if err != nil {
				return Result{}, err
			}
			part.Value = value
			part.Size = size
		}
		p.parts = append(p.parts, part)
	}
}

type parser struct {
	r        io.Reader
	src      *countingReader
	boundary string
	delim    []byte
	sep      []byte
	limits   Limits
	parts    []Part
	pushback []byte
}

// readByte prefers bytes pushed back after a boundary match. This keeps
// boundary detection correct across read chunks without buffering whole
// parts in memory or spilling to a fixed temp directory.
func (p *parser) readByte() (byte, error) {
	if len(p.pushback) > 0 {
		b := p.pushback[0]
		p.pushback = p.pushback[1:]
		return b, nil
	}
	var buf [1]byte
	for {
		n, err := p.r.Read(buf[:])
		if n == 1 {
			return buf[0], nil
		}
		if err != nil {
			return 0, err
		}
	}
}

func (p *parser) readFull(buf []byte) error {
	for len(buf) > 0 {
		b, err := p.readByte()
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return err
		}
		buf[0] = b
		buf = buf[1:]
	}
	return nil
}

// validBoundary mirrors the bcharsnospace grammar from RFC 2046.
func validBoundary(b string) bool {
	if len(b) < 1 || len(b) > 70 {
		return false
	}
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.IndexByte("'()+_,-./:=? ", c) >= 0:
		default:
			return false
		}
	}
	return true
}

func (p *parser) consumeFirstBoundary() error {
	// RFC 2046 allows an optional CRLF before the first boundary (the
	// preamble). multipart.Writer omits it; accept both forms.
	first, err := p.readByte()
	if err != nil {
		return classify(err)
	}
	second, err := p.readByte()
	if err != nil {
		return classify(err)
	}
	if first == '\r' && second == '\n' {
		return classify(p.readFull(p.delim))
	}
	if first == p.delim[0] && second == p.delim[1] {
		return classify(p.readFull(p.delim[2:]))
	}
	return ErrMalformed
}

// consumeBoundarySuffix reads the bytes that follow "--boundary": "\r\n"
// introduces another part, "--\r\n" closes the stream.
func (p *parser) consumeBoundarySuffix() (bool, error) {
	var two [2]byte
	if err := p.readFull(two[:]); err != nil {
		return false, classify(err)
	}
	switch {
	case two[0] == '\r' && two[1] == '\n':
		return false, nil
	case two[0] == '-' && two[1] == '-':
		if err := p.readFull(two[:]); err != nil {
			return false, classify(err)
		}
		if two[0] != '\r' || two[1] != '\n' {
			return false, ErrMalformed
		}
		return true, nil
	default:
		return false, ErrMalformed
	}
}

const maxHeaderBlock = 64 << 10

func (p *parser) readHeaders() (map[string]string, error) {
	headers := make(map[string]string)
	var line bytes.Buffer
	total := 0
	for {
		b, err := p.readByte()
		if err != nil {
			return nil, classify(err)
		}
		total++
		if total > maxHeaderBlock {
			return nil, fmt.Errorf("%w: headers too large", ErrLimitExceeded)
		}
		if b != '\n' {
			line.WriteByte(b)
			if line.Len() > maxHeaderBlock {
				return nil, fmt.Errorf("%w: headers too large", ErrLimitExceeded)
			}
			continue
		}
		raw, ok := bytes.CutSuffix(line.Bytes(), []byte("\r"))
		line.Reset()
		if !ok {
			return nil, ErrMalformed
		}
		if len(raw) == 0 {
			return headers, nil
		}
		name, value, found := bytes.Cut(raw, []byte(":"))
		if !found {
			return nil, ErrMalformed
		}
		headerName := http.CanonicalHeaderKey(strings.TrimSpace(string(name)))
		headerValue := strings.TrimSpace(string(value))
		if headerName == "" || !validHeaderValue(headerValue) {
			return nil, ErrMalformed
		}
		if _, exists := headers[headerName]; !exists {
			headers[headerName] = headerValue
		}
	}
}

func validHeaderValue(v string) bool {
	for _, c := range v {
		if c == 0 || c == '\r' || c == '\n' {
			return false
		}
	}
	return true
}

func buildPart(headers map[string]string) (part Part, isFile bool, err error) {
	raw, ok := headers["Content-Disposition"]
	if !ok {
		return Part{}, false, ErrMalformed
	}
	media, params, perr := mime.ParseMediaType(raw)
	if perr != nil || !strings.EqualFold(media, "form-data") {
		return Part{}, false, ErrMalformed
	}
	name := params["name"]
	if name == "" {
		return Part{}, false, ErrMalformed
	}
	part.Name = name
	if filename, present := params["filename"]; present {
		if err := validateFilename(filename); err != nil {
			return Part{}, false, err
		}
		part.Filename = filename
		return part, true, nil
	}
	return part, false, nil
}

// validateFilename rejects NUL bytes and paths that could escape a target
// directory: absolute paths, Windows drive letters, and any ".." segment.
func validateFilename(name string) error {
	if strings.ContainsRune(name, 0) {
		return ErrInvalidFilename
	}
	if filepath.IsAbs(name) {
		return ErrInvalidFilename
	}
	if len(name) >= 2 && name[1] == ':' {
		c := name[0]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			return ErrInvalidFilename
		}
	}
	clean := strings.ReplaceAll(name, "\\", "/")
	for _, segment := range strings.Split(clean, "/") {
		if segment == ".." {
			return ErrInvalidFilename
		}
	}
	return nil
}

// streamPart copies one part body to w, stopping exactly at the boundary
// separator. Framing bytes read ahead stay in pushback. It returns the number
// of body bytes written and fails once limit is exceeded.
func (p *parser) streamPart(w io.Writer, limit int64) (int64, error) {
	var size int64
	var pending []byte
	buf := make([]byte, 32<<10)
	for {
		n, rerr := p.readChunk(buf)
		if n > 0 {
			data := append(pending, buf[:n]...)
			if i := bytes.Index(data, p.sep); i >= 0 {
				if err := writeCapped(w, data[:i], limit, &size); err != nil {
					return size, err
				}
				p.pushback = append(p.pushback, data[i+len(p.sep):]...)
				return size, nil
			}
			safe := len(data) - (len(p.sep) - 1)
			if safe > 0 {
				if err := writeCapped(w, data[:safe], limit, &size); err != nil {
					return size, err
				}
			}
			pending = append(pending[:0], data[max(safe, 0):]...)
		}
		if rerr != nil {
			if rerr == io.EOF {
				// Stream ended without a closing boundary.
				return size, ErrMalformed
			}
			return size, rerr
		}
	}
}

// readChunk serves pushback bytes before reading the underlying stream, so
// bytes read past a boundary feed the following suffix or headers.
func (p *parser) readChunk(buf []byte) (int, error) {
	if len(p.pushback) > 0 {
		n := copy(buf, p.pushback)
		p.pushback = p.pushback[n:]
		return n, nil
	}
	return p.r.Read(buf)
}

func writeCapped(w io.Writer, data []byte, limit int64, size *int64) error {
	truncated := false
	if int64(len(data)) > limit-*size {
		data = data[:max(limit-*size, 0)]
		truncated = true
	}
	for len(data) > 0 {
		n, err := w.Write(data)
		*size += int64(n)
		data = data[n:]
		if err != nil {
			return err
		}
	}
	if truncated {
		return fmt.Errorf("%w: part too large", ErrLimitExceeded)
	}
	return nil
}

func (p *parser) readField() (string, int64, error) {
	var b bytes.Buffer
	size, err := p.streamPart(&b, p.limits.MaxFieldBytes)
	if err != nil {
		return "", 0, err
	}
	value := b.String()
	if !utf8.ValidString(value) {
		return "", 0, ErrMalformed
	}
	return value, size, nil
}

func (p *parser) streamFile() (int64, string, error) {
	h := sha256.New()
	size, err := p.streamPart(h, p.limits.MaxFileBytes)
	if err != nil {
		return 0, "", err
	}
	return size, hex.EncodeToString(h.Sum(nil)), nil
}

type countingReader struct {
	r   io.Reader
	n   int64
	max int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	if c.n > c.max {
		return n, fmt.Errorf("%w: request body too large", ErrLimitExceeded)
	}
	return n, err
}

// classify turns unexpected EOF at the framing layer into ErrMalformed while
// preserving other errors such as context cancellation.
func classify(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrLimitExceeded) || errors.Is(err, ErrInvalidContentType) ||
		errors.Is(err, ErrInvalidFilename) || errors.Is(err, ErrMalformed) {
		return err
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return ErrMalformed
	}
	return err
}
