package adapter

import (
	"errors"
	"net/http"
	"testing"
)

func TestRegisterAndGet(t *testing.T) {
	r := NewAdapterRegistry()
	r.Register(NewOpenAIAdapter())
	got, err := r.Get("openai")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Name() != "openai" {
		t.Errorf("Name() = %q, want openai", got.Name())
	}
}

func TestGetUnknown(t *testing.T) {
	r := NewAdapterRegistry()
	_, err := r.Get("unknown-provider")
	if !errors.Is(err, ErrAdapterNotFound) {
		t.Errorf("Get() error = %v, want ErrAdapterNotFound", err)
	}
}

func TestRegisterOverwrites(t *testing.T) {
	r := NewAdapterRegistry()
	r.Register(NewOpenAIAdapter())
	r.Register(&fakeAdapter{name: "openai"})
	got, err := r.Get("openai")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.(*fakeAdapter); !ok {
		t.Errorf("overwrite failed, got %T, want *fakeAdapter", got)
	}
}

// fakeAdapter 用于覆盖注册测试的替代适配器
type fakeAdapter struct {
	name string
}

func (a *fakeAdapter) Name() string              { return a.name }
func (a *fakeAdapter) SupportsNativeProxy() bool { return false }
func (a *fakeAdapter) TransformRequest(req *UnifiedRequest, rawBody []byte) (*http.Request, error) {
	return nil, errors.New("not implemented")
}
func (a *fakeAdapter) TransformResponse(resp *http.Response) (*UnifiedResponse, error) {
	return nil, errors.New("not implemented")
}
func (a *fakeAdapter) TransformStreamChunk(chunk []byte) (*UnifiedSSEChunk, error) {
	return nil, errors.New("not implemented")
}
func (a *fakeAdapter) ParseTokenUsage(resp *http.Response) (int, int, int) { return 0, 0, 0 }
func (a *fakeAdapter) ParseStreamUsage(chunk []byte) (int, int, int)       { return 0, 0, 0 }
func (a *fakeAdapter) ParseError(resp *http.Response) (int, string)        { return 0, "" }
