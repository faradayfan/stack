package config

import (
	"reflect"
	"testing"
)

// TestMergeTree exercises the one merge rule: maps merge by key, scalars replace,
// lists replace. These are the cases the whole schema relies on.
func TestMergeTree(t *testing.T) {
	cases := []struct {
		name           string
		base, override map[string]any
		want           map[string]any
	}{
		{
			name:     "scalar replaces",
			base:     map[string]any{"delivery": "load"},
			override: map[string]any{"delivery": "push"},
			want:     map[string]any{"delivery": "push"},
		},
		{
			name:     "new key adds",
			base:     map[string]any{"tool": "docker"},
			override: map[string]any{"delivery": "push"},
			want:     map[string]any{"tool": "docker", "delivery": "push"},
		},
		{
			name:     "map merges by key (untouched key kept)",
			base:     map[string]any{"deliver": map[string]any{"tool": "docker", "delivery": "load"}},
			override: map[string]any{"deliver": map[string]any{"delivery": "push"}},
			want:     map[string]any{"deliver": map[string]any{"tool": "docker", "delivery": "push"}},
		},
		{
			name:     "list replaces (not appends)",
			base:     map[string]any{"values": []any{"a.yaml"}},
			override: map[string]any{"values": []any{"b.yaml", "c.yaml"}},
			want:     map[string]any{"values": []any{"b.yaml", "c.yaml"}},
		},
		{
			name:     "set map merges, sibling key untouched",
			base:     map[string]any{"apply": map[string]any{"chart": "x", "set": map[string]any{"a": "1"}}},
			override: map[string]any{"apply": map[string]any{"set": map[string]any{"b": "2"}}},
			want:     map[string]any{"apply": map[string]any{"chart": "x", "set": map[string]any{"a": "1", "b": "2"}}},
		},
		{
			name:     "scalar overrides a base map (type change)",
			base:     map[string]any{"x": map[string]any{"k": "v"}},
			override: map[string]any{"x": "scalar"},
			want:     map[string]any{"x": "scalar"},
		},
		{
			name:     "null clears a key (delete from result)",
			base:     map[string]any{"tag": "ollama", "args": map[string]any{"PATCH": "1"}},
			override: map[string]any{"tag": "openai", "args": nil},
			want:     map[string]any{"tag": "openai"},
		},
		{
			name:     "null on an absent key is a no-op (not added)",
			base:     map[string]any{"tag": "x"},
			override: map[string]any{"args": nil},
			want:     map[string]any{"tag": "x"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeTree(tc.base, tc.override)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("mergeTree:\n got  %#v\n want %#v", got, tc.want)
			}
		})
	}
}

// TestMergeTree_DoesNotMutateBase: merging must not mutate the base template (it
// is reused across envs).
func TestMergeTree_DoesNotMutateBase(t *testing.T) {
	base := map[string]any{"deliver": map[string]any{"tool": "docker", "delivery": "load"}}
	_ = mergeTree(base, map[string]any{"deliver": map[string]any{"delivery": "push"}})
	inner := base["deliver"].(map[string]any)
	if inner["delivery"] != "load" {
		t.Errorf("base mutated: deliver.delivery = %v, want load", inner["delivery"])
	}
}
