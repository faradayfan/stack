package plugins

import "testing"

func TestValidateConfig(t *testing.T) {
	m := Manifest{
		Tool: "helm",
		Config: []ConfigKey{
			{Name: "chart", Required: true},
			{Name: "values"},
		},
	}
	// required present, known keys → ok
	if err := m.ValidateConfig(map[string]any{"chart": "c", "values": []any{"a"}}); err != nil {
		t.Errorf("valid config rejected: %v", err)
	}
	// missing required → error
	if err := m.ValidateConfig(map[string]any{"values": []any{"a"}}); err == nil {
		t.Error("missing required `chart` should error")
	}
	// unknown key (typo) → error
	if err := m.ValidateConfig(map[string]any{"chart": "c", "valeus": "x"}); err == nil {
		t.Error("unknown key `valeus` should error")
	}
	// a manifest with no declared config accepts anything
	open := Manifest{Tool: "x"}
	if err := open.ValidateConfig(map[string]any{"whatever": 1}); err != nil {
		t.Errorf("undeclared-config manifest should accept anything: %v", err)
	}
}
