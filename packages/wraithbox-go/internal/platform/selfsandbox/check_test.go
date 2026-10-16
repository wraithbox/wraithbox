package selfsandbox

import "testing"

// TestCheckProfile feeds the check before the call each bad profile and
// parameter set, and sees each refused with its rule
// (X23-sandboxed-daemons, conditions 5 and 7). It runs on every OS: the
// check confines nothing.
func TestCheckProfile(t *testing.T) {
	const withParam = `(version 1) (deny default) (allow file-read* (subpath (param "DATA_DIR")))`
	tests := []struct {
		name   string
		text   string
		params map[string]string
		rule   Rule
	}{
		{name: "no parameters", text: "(version 1) (deny default)"},
		{name: "one parameter", text: withParam, params: map[string]string{"DATA_DIR": "/private/var/x"}},
		{
			name:   "a parameter named twice",
			text:   withParam + ` (allow file-write* (subpath (param "DATA_DIR")))`,
			params: map[string]string{"DATA_DIR": "/x"},
		},
		{name: "empty text", text: "", rule: RuleProfileInvalid},
		{name: "NUL in the text", text: "(version 1)\x00(allow default)", rule: RuleProfileInvalid},
		{name: "missing", text: withParam, params: nil, rule: RuleParamMissing},
		{name: "extra", text: withParam, params: map[string]string{"DATA_DIR": "/x", "OTHER": "/y"}, rule: RuleParamUnexpected},
		{name: "extra without any in the text", text: "(version 1)", params: map[string]string{"OTHER": "/y"}, rule: RuleParamUnexpected},
		{name: "empty value", text: withParam, params: map[string]string{"DATA_DIR": ""}, rule: RuleParamInvalid},
		{name: "NUL in a value", text: withParam, params: map[string]string{"DATA_DIR": "/x\x00/y"}, rule: RuleParamInvalid},
		{name: "lowercase name", text: `(allow file-read* (subpath (param "x")))`, params: map[string]string{"x": "/x"}, rule: RuleProfileParamForm},
		{name: "space before param", text: `(allow file-read* (subpath ( param "X")))`, params: map[string]string{"X": "/x"}, rule: RuleProfileParamForm},
		{name: "param from another call", text: `(allow file-read* (subpath (param (string-append "X" "Y"))))`, rule: RuleProfileParamForm},
		{name: "name with a digit first", text: `(subpath (param "1X"))`, params: map[string]string{"1X": "/x"}, rule: RuleProfileParamForm},
		{name: "no closing parenthesis", text: `(subpath (param "X" "Y"))`, params: map[string]string{"X": "/x"}, rule: RuleProfileParamForm},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := checkProfile(Profile{Name: "test", Text: tt.text}, tt.params)
			if tt.rule == "" {
				if r != nil {
					t.Fatalf("refused: %v", r)
				}
				return
			}
			if r == nil || r.Rule != tt.rule {
				t.Fatalf("got %v, want rule %s", r, tt.rule)
			}
		})
	}
}
