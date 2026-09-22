package main

import (
	"fmt"
	"github.com/huangjie666777-ux/schema-registry-015/registry"
)

func main() {
	s := registry.NewStore()
	v, err := s.Register("demo", "orders", []byte(`{"type":"object","properties":{"id":{"type":"string"}}}`), nil)
	if err != nil {
		panic(err)
	}
	fmt.Printf("registered v%d fingerprint=%s\n", v.Number, v.Fingerprint)
}
