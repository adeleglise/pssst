package source

import (
	"testing"
	"time"
)

func TestEveryAdapterBuilds(t *testing.T) {
	for _, name := range Types() {
		adapter, found := Lookup(name)
		if !found || adapter.New == nil {
			t.Fatalf("%s has no constructor", name)
		}
		if adapter.New("https://status.example.test", nil, nil, time.Second) == nil {
			t.Fatalf("%s built a nil provider", name)
		}
	}
	if _, found := Lookup(None); found {
		t.Fatal("none must never have an adapter: absent is not healthy")
	}
}
