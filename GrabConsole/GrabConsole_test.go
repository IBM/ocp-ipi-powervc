package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestReadMetadata(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		wantInfra string
		wantCloud string
		wantErr   string
	}{
		{"openstack section", `{"infraID":"i1","openstack":{"cloud":"os-cloud"}}`, "i1", "os-cloud", ""},
		{"powervc section only", `{"infraID":"i1","powervc":{"cloud":"pv-cloud"}}`, "i1", "pv-cloud", ""},
		{"openstack wins over powervc", `{"infraID":"i1","openstack":{"cloud":"os-cloud"},"powervc":{"cloud":"pv-cloud"}}`, "i1", "os-cloud", ""},
		{"empty openstack falls back to powervc", `{"infraID":"i1","openstack":{"cloud":""},"powervc":{"cloud":"pv-cloud"}}`, "i1", "pv-cloud", ""},
		{"no cloud", `{"infraID":"i1"}`, "", "", "neither openstack.cloud nor powervc.cloud"},
		{"no infraID", `{"powervc":{"cloud":"pv-cloud"}}`, "", "", "infraID is empty"},
		{"bad json", `{`, "", "", "cannot parse"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "metadata.json"), []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			infra, cloud, err := readMetadata(dir)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if infra != tc.wantInfra || cloud != tc.wantCloud {
				t.Errorf("got (%q, %q), want (%q, %q)", infra, cloud, tc.wantInfra, tc.wantCloud)
			}
		})
	}
}
