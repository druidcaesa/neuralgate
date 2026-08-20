package core

import (
	"context"
	"testing"
)

func TestRequestContextRoundTrip(t *testing.T) {
	ctx := context.Background()
	rc := &RequestContext{RequestID: "r1"}
	ctx = WithRequestContext(ctx, rc)
	got, ok := RequestContextFrom(ctx)
	if !ok {
		t.Fatal("RequestContextFrom() ok = false, want true")
	}
	if got != rc {
		t.Error("RequestContextFrom() returned different pointer")
	}
}

func TestRequestContextFromEmpty(t *testing.T) {
	_, ok := RequestContextFrom(context.Background())
	if ok {
		t.Error("RequestContextFrom() ok = true on empty context, want false")
	}
}
