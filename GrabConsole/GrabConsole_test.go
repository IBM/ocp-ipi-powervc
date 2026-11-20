package main

import "testing"

func TestEscapeFilter(t *testing.T) {
	tests := []struct {
		name       string
		chunks     []string
		wantOut    string
		wantDiscon bool
	}{
		{"plain text passes through", []string{"root\r"}, "root\r", false},
		{"tilde-dot at start disconnects", []string{"~."}, "", true},
		{"tilde-dot after enter disconnects", []string{"user\r~."}, "user\r", true},
		{"tilde-dot after newline disconnects", []string{"a\n~."}, "a\n", true},
		{"tilde-dot mid-line is forwarded", []string{"ab~."}, "ab~.", false},
		{"tilde-tilde sends one tilde", []string{"~~"}, "~", false},
		{"tilde then other is forwarded", []string{"~x"}, "~x", false},
		{"escape split across reads", []string{"x\r~", "."}, "x\r", true},
		{"tilde held until next byte", []string{"~"}, "", false},
		{"bytes after escape are dropped", []string{"~.rest"}, "", true},
		{"tilde-tilde then dot is literal", []string{"~~."}, "~.", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var f escapeFilter
			var got []byte
			var disconnected bool
			for _, c := range tc.chunks {
				out, d := f.process([]byte(c))
				got = append(got, out...)
				if d {
					disconnected = true
					break
				}
			}
			if string(got) != tc.wantOut || disconnected != tc.wantDiscon {
				t.Errorf("got out=%q disconnect=%v, want out=%q disconnect=%v",
					got, disconnected, tc.wantOut, tc.wantDiscon)
			}
		})
	}
}
