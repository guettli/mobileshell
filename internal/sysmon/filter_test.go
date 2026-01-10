package sysmon

import (
	"testing"
)

func TestMatchesSearch(t *testing.T) {
	tests := []struct {
		text     string
		query    string
		expected bool
	}{
		{"python script.py", "python", true},
		{"python script.py", "PYTHON", true},
		{"python script.py", "script", true},
		{"python script.py", "python script", true},
		{"python script.py", "script python", true},
		{"python script.py", "go", false},
		{"python script.py", "python go", false},
		{"bash", "", true},
		{"bash", "  ", true},
		{"/usr/bin/python3 main.py", "python main", true},
		{"/usr/bin/python3 main.py", "bin python3", true},
	}

	for _, tt := range tests {
		result := matchesSearch(tt.text, tt.query)
		if result != tt.expected {
			t.Errorf("matchesSearch(%q, %q) = %v; want %v", tt.text, tt.query, result, tt.expected)
		}
	}
}
