package adapter

import "testing"

func TestBuiltinAdapters(t *testing.T) {
	cases := []struct {
		name    string
		adapter ModelAdapter
		native  bool
	}{
		{"openai", NewOpenAIAdapter(), true},
		{"tongyi", NewTongyiAdapter(), false},
		{"zhipu", NewZhipuAdapter(), false},
		{"deepseek", NewDeepSeekAdapter(), true},
	}
	for _, c := range cases {
		if c.adapter.Name() != c.name {
			t.Errorf("%s Name() = %q", c.name, c.adapter.Name())
		}
		if c.adapter.SupportsNativeProxy() != c.native {
			t.Errorf("%s SupportsNativeProxy() = %v, want %v", c.name, c.adapter.SupportsNativeProxy(), c.native)
		}
	}
}
