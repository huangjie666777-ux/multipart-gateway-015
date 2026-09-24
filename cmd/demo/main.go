package main

import (
	"fmt"
	"mime/multipart"
	"net/textproto"
	"strings"

	"github.com/huangjie666777-ux/multipart-gateway-015/forms"
)

func main() {
	var b strings.Builder
	w := multipart.NewWriter(&b)
	_ = w.WriteField("title", "demo")
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", "form-data; name=\"file\"; filename=\"hello.txt\"")
	h.Set("Content-Type", "text/plain")
	p, _ := w.CreatePart(h)
	_, _ = p.Write([]byte("hello"))
	_ = w.Close()
	result, err := forms.Parse(w.FormDataContentType(), strings.NewReader(b.String()), forms.DefaultLimits())
	if err != nil {
		panic(err)
	}
	fmt.Printf("parts=%d\n", len(result.Parts))
	for i, part := range result.Parts {
		fmt.Printf("  [%d] %+v\n", i, part)
	}
}
