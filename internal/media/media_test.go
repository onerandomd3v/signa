package media

import (
	"strings"
	"testing"
)

func TestValidateMedia(t *testing.T) {
	tests := []struct {
		name    string
		kind    string
		mime    string
		size    int64
		wantErr bool
	}{
		{"image accepted", TypeImage, "image/png", 1024, false},
		{"audio accepted", TypeAudio, "audio/mpeg", 1024, false},
		{"video accepted", TypeVideo, "video/mp4", 1024, false},
		{"type rejected", "document", "application/pdf", 1024, true},
		{"mime rejected", TypeImage, "image/svg+xml", 1024, true},
		{"size rejected", TypeVideo, "video/mp4", 100<<20 + 1, true},
		{"empty rejected", TypeImage, "image/png", 0, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := Validate(test.kind, test.mime, test.size); (err != nil) != test.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestObjectKeyNamespace(t *testing.T) {
	key := ObjectKey("report-1")
	if !strings.HasPrefix(key, "reports/report-1/media/") {
		t.Fatalf("key = %q, want report namespace", key)
	}
	if !IsObjectKeyForReport(key, "report-1") {
		t.Fatal("generated key was not recognized for its report")
	}
	if IsObjectKeyForReport("reports/report-2/media/object", "report-1") || IsObjectKeyForReport("reports/report-1/media/a/b", "report-1") {
		t.Fatal("foreign or nested key was accepted")
	}
}
