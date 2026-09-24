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
	"strings"
	"unicode/utf8"
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

const maxHeaderBytes = 32 << 10

// countReader enforces the total request byte budget.
type countReader struct {
	r     io.Reader
	total int64
	max   int64
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.total += int64(n)
	if c.total > c.max {
		return n, ErrLimitExceeded
	}
	return n, err
}

// stream reads from the body with a pushback buffer, so bytes scanned past
// a delimiter can be handed back to the next parsing stage.
type stream struct {
	br      *bufio.Reader
	pending []byte
}

func (s *stream) Read(p []byte) (int, error) {
	if len(s.pending) > 0 {
		n := copy(p, s.pending)
		s.pending = s.pending[n:]
		return n, nil
	}
	return s.br.Read(p)
}

func (s *stream) ReadByte() (byte, error) {
	if len(s.pending) > 0 {
		b := s.pending[0]
		s.pending = s.pending[1:]
		return b, nil
	}
	return s.br.ReadByte()
}

func (s *stream) pushback(b []byte) {
	s.pending = append(b, s.pending...)
}

// Parse streams a multipart/form-data body, enforcing limits and returning
// parts in order of appearance. File contents are never stored; only size
// and a SHA-256 digest over the complete file bytes are kept.
func Parse(contentType string, body io.Reader, limits Limits) (Result, error) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.EqualFold(mediaType, "multipart/form-data") || params["boundary"] == "" {
		return Result{}, ErrInvalidContentType
	}
	boundary := params["boundary"]
	if len(boundary) > 200 {
		return Result{}, ErrInvalidContentType
	}

	s := &stream{br: bufio.NewReaderSize(&countReader{r: body, max: limits.MaxBytes}, 32*1024)}

	// The body must start directly with the first delimiter; no preamble.
	first := "--" + boundary
	head := make([]byte, len(first))
	if _, err := io.ReadFull(s, head); err != nil {
		return Result{}, mapEOF(err)
	}
	if string(head) != first {
		return Result{}, ErrMalformed
	}

	result := Result{Parts: []Part{}}
	delim := "\r\n--" + boundary

	for {
		// After a delimiter: either CRLF (another part) or "--" (close).
		mark, err := s.ReadByte()
		if err != nil {
			return Result{}, mapEOF(err)
		}
		if mark == '-' {
			second, err := s.ReadByte()
			if err != nil {
				return Result{}, mapEOF(err)
			}
			if second != '-' {
				return Result{}, ErrMalformed
			}
			// Closing delimiter: allow one optional CRLF, then EOF only.
			// Anything else (e.g. a repeated terminator) is malformed.
			tail, err := io.ReadAll(s)
			if err != nil {
				return Result{}, err
			}
			if len(tail) != 0 && string(tail) != "\r\n" {
				return Result{}, ErrMalformed
			}
			return result, nil
		}
		if mark != '\r' {
			return Result{}, ErrMalformed
		}
		nl, err := s.ReadByte()
		if err != nil {
			return Result{}, mapEOF(err)
		}
		if nl != '\n' {
			return Result{}, ErrMalformed
		}

		if len(result.Parts) >= limits.MaxParts {
			return Result{}, ErrLimitExceeded
		}
		part, err := readPart(s, delim, limits)
		if err != nil {
			return Result{}, err
		}
		result.Parts = append(result.Parts, part)
	}
}

func mapEOF(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return ErrMalformed
	}
	return err
}

func readPart(s *stream, delim string, limits Limits) (Part, error) {
	header, err := readHeader(s)
	if err != nil {
		return Part{}, err
	}
	disp, params, err := mime.ParseMediaType(header["content-disposition"])
	if err != nil || !strings.EqualFold(disp, "form-data") {
		return Part{}, ErrMalformed
	}
	name := params["name"]
	if name == "" || strings.ContainsRune(name, 0) {
		return Part{}, ErrMalformed
	}
	filename, isFile := params["filename"]
	if isFile {
		if err := checkFilename(filename); err != nil {
			return Part{}, err
		}
	}

	if isFile {
		return readFilePart(s, delim, limits, name, filename)
	}
	return readFieldPart(s, delim, limits, name)
}

func readHeader(s *stream) (map[string]string, error) {
	h := map[string]string{}
	read := 0
	for {
		line, err := readLine(s, maxHeaderBytes-read)
		if err != nil {
			return nil, err
		}
		read += len(line) + 2
		if line == "" {
			return h, nil
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok || key == "" || key != strings.TrimSpace(key) {
			return nil, ErrMalformed
		}
		h[strings.ToLower(key)] = strings.TrimSpace(value)
	}
}

// readLine reads one CRLF-terminated line, returned without the CRLF.
func readLine(s *stream, maxLeft int) (string, error) {
	if maxLeft <= 0 {
		return "", ErrLimitExceeded
	}
	var b strings.Builder
	var prev byte
	for {
		c, err := s.ReadByte()
		if err != nil {
			return "", mapEOF(err)
		}
		if b.Len() >= maxLeft {
			return "", ErrLimitExceeded
		}
		if c == '\n' {
			if prev != '\r' {
				return "", ErrMalformed
			}
			line := b.String()
			return line[:len(line)-1], nil
		}
		b.WriteByte(c)
		prev = c
	}
}

func checkFilename(name string) error {
	if name == "" || name == "." || name == ".." ||
		strings.ContainsRune(name, 0) ||
		strings.ContainsAny(name, "/\\") {
		return ErrInvalidFilename
	}
	return nil
}

// scanBody streams part content until delim, invoking emit per chunk. The
// delimiter may span read chunks; a tail of len(delim) bytes is held back.
func scanBody(s *stream, delim string, emit func([]byte) error) error {
	tmp := make([]byte, 32*1024)
	var carry []byte
	d := []byte(delim)
	for {
		n, err := s.Read(tmp)
		data := append(carry, tmp[:n]...)
		if idx := bytes.Index(data, d); idx >= 0 {
			if e := emit(data[:idx]); e != nil {
				return e
			}
			s.pushback(data[idx+len(d):])
			return nil
		}
		if err != nil {
			return mapEOF(err)
		}
		keep := len(d)
		if len(data) <= keep {
			carry = data
			continue
		}
		if e := emit(data[:len(data)-keep]); e != nil {
			return e
		}
		carry = append([]byte(nil), data[len(data)-keep:]...)
	}
}

func readFieldPart(s *stream, delim string, limits Limits, name string) (Part, error) {
	var buf bytes.Buffer
	overflow := false
	err := scanBody(s, delim, func(chunk []byte) error {
		if overflow {
			return nil
		}
		if int64(buf.Len())+int64(len(chunk)) > limits.MaxFieldBytes {
			overflow = true
			return nil
		}
		buf.Write(chunk)
		return nil
	})
	if err != nil {
		return Part{}, err
	}
	if overflow {
		return Part{}, ErrLimitExceeded
	}
	value := buf.String()
	if !utf8.ValidString(value) {
		return Part{}, fmt.Errorf("%w: field %q is not valid UTF-8", ErrMalformed, name)
	}
	return Part{Name: name, Value: value, Size: int64(buf.Len())}, nil
}

func readFilePart(s *stream, delim string, limits Limits, name, filename string) (Part, error) {
	hash := sha256.New()
	var size int64
	overflow := false
	err := scanBody(s, delim, func(chunk []byte) error {
		if overflow {
			return nil
		}
		if size+int64(len(chunk)) > limits.MaxFileBytes {
			overflow = true
			return nil
		}
		size += int64(len(chunk))
		hash.Write(chunk)
		return nil
	})
	if err != nil {
		return Part{}, err
	}
	if overflow {
		return Part{}, ErrLimitExceeded
	}
	return Part{
		Name:     name,
		Filename: filename,
		Size:     size,
		SHA256:   hex.EncodeToString(hash.Sum(nil)),
	}, nil
}
