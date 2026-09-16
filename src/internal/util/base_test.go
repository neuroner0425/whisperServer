package util

import "testing"

func TestNormalizeTimelineTimestamp(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "already standard format",
			input:    "[00:01:23,456]",
			expected: "00:01:23,456",
		},
		{
			name:     "short minute:second without hour",
			input:    "[00:15,860]",
			expected: "00:00:15,860",
		},
		{
			name:     "dot instead of comma",
			input:    "[01:23:45.678]",
			expected: "01:23:45,678",
		},
		{
			name:     "single digit segments",
			input:    "1:2:3,4",
			expected: "01:02:03,400",
		},
		{
			name:     "no brackets with spaces",
			input:    "  02:30,50  ",
			expected: "00:02:30,500",
		},
		{
			name:     "four digit millisecond truncated",
			input:    "00:01:02,1234",
			expected: "00:01:02,123",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizeTimelineTimestamp(tc.input)
			if got != tc.expected {
				t.Fatalf("NormalizeTimelineTimestamp(%q) = %q, expected %q", tc.input, got, tc.expected)
			}
		})
	}
}
