package registry

import "testing"

func TestCanonicalJSON(t *testing.T) {
	got, err := CanonicalJSON([]byte(`{"b":2,"a":{"d":4,"c":3}}`))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":{"c":3,"d":4},"b":2}`
	if string(got) != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestTenantIsolation(t *testing.T) {
	s := NewStore()
	if _, err := s.Register("one", "user", []byte(`{"type":"object"}`), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Latest("two", "user"); err != ErrNotFound {
		t.Fatalf("expected isolation, got %v", err)
	}
}
