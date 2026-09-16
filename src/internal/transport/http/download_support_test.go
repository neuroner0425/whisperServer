package httptransport

import (
	"testing"
)

func TestFormatContentDisposition(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		wantSub1 string
		wantSub2 string
	}{
		{
			name:     "ASCII filename",
			filename: "report.zip",
			wantSub1: `filename="report.zip"`,
			wantSub2: `filename*=UTF-8''report.zip`,
		},
		{
			name:     "Korean filename",
			filename: "회의록_2026.zip",
			wantSub1: `filename="____2026.zip"`,
			wantSub2: `filename*=UTF-8''%ED%9A%8C%EC%9D%98%EB%A1%9D_2026.zip`,
		},
		{
			name:     "Filename with spaces and quotes",
			filename: `my "cool" notes.md`,
			wantSub1: `filename="my _cool_ notes.md"`,
			wantSub2: `filename*=UTF-8''my%20%22cool%22%20notes.md`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatContentDisposition(tc.filename)
			if !testing.Short() {
				// verify contains wantSub1 and wantSub2
				if len(got) == 0 {
					t.Fatalf("expected non-empty header value")
				}
			}
			if !stringsContains(got, tc.wantSub1) {
				t.Errorf("got %q, want it to contain %q", got, tc.wantSub1)
			}
			if !stringsContains(got, tc.wantSub2) {
				t.Errorf("got %q, want it to contain %q", got, tc.wantSub2)
			}
		})
	}
}

func stringsContains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || (len(s) > 0 && len(substr) > 0 && findSubstr(s, substr)))
}

func findSubstr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
