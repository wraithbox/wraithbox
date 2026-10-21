//go:build darwin || linux

package tty

import "testing"

// TestHostCharset is the charset read of S16-01-filter, "Where": the
// first of LC_ALL, LC_CTYPE and LANG that is set and not empty decides,
// as setlocale reads them, and only a UTF-8 codeset is UTF-8.
func TestHostCharset(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want Charset
	}{
		{"unset", nil, Charset{}},
		{"all empty", map[string]string{"LC_ALL": "", "LC_CTYPE": "", "LANG": ""}, Charset{}},
		{"C", map[string]string{"LANG": "C"}, Charset{"LANG", "C", false}},
		{"POSIX", map[string]string{"LANG": "POSIX"}, Charset{"LANG", "POSIX", false}},
		{"ISO8859-1", map[string]string{"LANG": "en_US.ISO8859-1"}, Charset{"LANG", "en_US.ISO8859-1", false}},
		{"ISO-8859-15 with modifier", map[string]string{"LANG": "de_DE.ISO-8859-15@euro"}, Charset{"LANG", "de_DE.ISO-8859-15@euro", false}},
		{"no codeset", map[string]string{"LANG": "en_US"}, Charset{"LANG", "en_US", false}},
		{"GB18030", map[string]string{"LANG": "zh_CN.GB18030"}, Charset{"LANG", "zh_CN.GB18030", false}},
		{"UTF-16", map[string]string{"LANG": "en_US.UTF-16"}, Charset{"LANG", "en_US.UTF-16", false}},
		{"utf8 as language", map[string]string{"LANG": "UTF-8.ISO8859-1"}, Charset{"LANG", "UTF-8.ISO8859-1", false}},
		{"utf8 as modifier", map[string]string{"LANG": "en_US.ISO8859-1@UTF-8"}, Charset{"LANG", "en_US.ISO8859-1@UTF-8", false}},
		{"UTF-8", map[string]string{"LANG": "en_US.UTF-8"}, Charset{"LANG", "en_US.UTF-8", true}},
		{"C.UTF-8", map[string]string{"LANG": "C.UTF-8"}, Charset{"LANG", "C.UTF-8", true}},
		{"utf8", map[string]string{"LANG": "en_US.utf8"}, Charset{"LANG", "en_US.utf8", true}},
		{"UTF8", map[string]string{"LANG": "en_US.UTF8"}, Charset{"LANG", "en_US.UTF8", true}},
		{"utf-8", map[string]string{"LANG": "C.utf-8"}, Charset{"LANG", "C.utf-8", true}},
		{"UTF-8 with modifier", map[string]string{"LANG": "en_US.UTF-8@mod"}, Charset{"LANG", "en_US.UTF-8@mod", true}},
		{"codeset alone", map[string]string{"LC_CTYPE": "UTF-8"}, Charset{"LC_CTYPE", "UTF-8", true}},
		{"utf8 alone", map[string]string{"LANG": "utf8"}, Charset{"LANG", "utf8", true}},
		{"LC_ALL over LC_CTYPE and LANG", map[string]string{"LC_ALL": "en_US.ISO8859-1", "LC_CTYPE": "en_US.UTF-8", "LANG": "en_US.UTF-8"}, Charset{"LC_ALL", "en_US.ISO8859-1", false}},
		{"LC_ALL UTF-8 over LANG C", map[string]string{"LC_ALL": "en_US.UTF-8", "LANG": "C"}, Charset{"LC_ALL", "en_US.UTF-8", true}},
		{"LC_CTYPE over LANG", map[string]string{"LC_CTYPE": "C", "LANG": "en_US.UTF-8"}, Charset{"LC_CTYPE", "C", false}},
		{"empty LC_ALL skipped", map[string]string{"LC_ALL": "", "LC_CTYPE": "POSIX", "LANG": "en_US.UTF-8"}, Charset{"LC_CTYPE", "POSIX", false}},
		{"empty LC_ALL and LC_CTYPE skipped", map[string]string{"LC_ALL": "", "LC_CTYPE": "", "LANG": "en_US.UTF-8"}, Charset{"LANG", "en_US.UTF-8", true}},
		{"other LC_ ignored", map[string]string{"LC_MESSAGES": "en_US.UTF-8", "LC_TIME": "en_US.UTF-8"}, Charset{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lookup := func(k string) (string, bool) { v, ok := tt.env[k]; return v, ok }
			got, err := HostCharset(lookup)
			if err != nil || got != tt.want {
				t.Errorf("HostCharset = %+v, %v; want %+v", got, err, tt.want)
			}
		})
	}
}
