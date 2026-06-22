package config

import "gopkg.in/yaml.v3"

// The uniform merge rule (docs/SCHEMA-V2.md): override ▸ base.
//
//   - maps merge by key (recursively) — override an existing key, or add one;
//   - scalars replace — the override's value wins;
//   - lists replace — an override list replaces the base list (no append);
//   - null (nil) clears — an explicit null in the override deletes that key from
//     the result (the one way to remove an inherited value, e.g. a template
//     image's build args that a given env doesn't want).
//
// Everything in the schema (images, step blocks, apply.set, identity) follows
// this single rule, so the resolved result is never surprising.

// mergeTree returns base deep-merged with override (override wins).
func mergeTree(base, override map[string]any) map[string]any {
	out := make(map[string]any, len(base))
	for k, v := range base {
		out[k] = v
	}
	for k, ov := range override {
		if ov == nil { // explicit null → delete the key
			delete(out, k)
			continue
		}
		if bv, ok := out[k]; ok {
			bm, bIsMap := asMap(bv)
			om, oIsMap := asMap(ov)
			if bIsMap && oIsMap {
				out[k] = mergeTree(bm, om) // recurse: maps merge by key
				continue
			}
		}
		out[k] = ov // scalar or list → replace; new key → add
	}
	return out
}

// asMap normalizes the two map shapes yaml.v3 produces (map[string]any from
// Decode-into-map, and the generic any maps) to map[string]any.
func asMap(v any) (map[string]any, bool) {
	switch m := v.(type) {
	case map[string]any:
		return m, true
	case map[any]any:
		out := make(map[string]any, len(m))
		for k, val := range m {
			ks, ok := k.(string)
			if !ok {
				return nil, false
			}
			out[ks] = val
		}
		return out, true
	default:
		return nil, false
	}
}

// readTree reads a YAML file into a generic map tree.
func readTree(path string) (map[string]any, error) {
	var out map[string]any
	if err := readYAML(path, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

// decodeTree decodes a generic map tree into a typed value (honoring custom
// UnmarshalYAML on the target, e.g. Pattern/StepBlock).
func decodeTree(tree map[string]any, dst any) error {
	b, err := yaml.Marshal(tree)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(b, dst)
}
