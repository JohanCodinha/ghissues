package fs

import (
	"testing"
)

func TestSanitizeTitle(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple title",
			input:    "Crash on startup",
			expected: "crash-on-startup",
		},
		{
			name:     "title with special characters",
			input:    "Bug: Can't login! (urgent)",
			expected: "bug-cant-login-urgent",
		},
		{
			name:     "title with multiple spaces",
			input:    "Multiple   spaces   here",
			expected: "multiple-spaces-here",
		},
		{
			name:     "title with numbers",
			input:    "Fix issue 123 in module",
			expected: "fix-issue-123-in-module",
		},
		{
			name:     "empty title",
			input:    "",
			expected: "issue",
		},
		{
			name:     "only special characters",
			input:    "!@#$%^&*()",
			expected: "issue",
		},
		{
			name:     "title longer than 50 chars",
			input:    "This is a very long title that exceeds the maximum length allowed for sanitized filenames",
			expected: "this-is-a-very-long-title-that-exceeds-the-maximum",
		},
		{
			name:     "title with leading/trailing spaces",
			input:    "  Leading and trailing  ",
			expected: "leading-and-trailing",
		},
		{
			name:     "title with unicode characters",
			input:    "Fix für Deutsch",
			expected: "fix-fr-deutsch",
		},
		{
			name:     "title with underscores",
			input:    "some_function_name broken",
			expected: "somefunctionname-broken",
		},
		{
			name:     "title ending in dash after truncation",
			input:    "this-is-a-title-that-ends-in-a-dash-after-truncate-x",
			expected: "this-is-a-title-that-ends-in-a-dash-after-truncate",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := sanitizeTitle(tc.input)
			if result != tc.expected {
				t.Errorf("sanitizeTitle(%q) = %q, expected %q", tc.input, result, tc.expected)
			}
		})
	}
}

func TestMakeFilename(t *testing.T) {
	tests := []struct {
		name     string
		title    string
		number   int
		expected string
	}{
		{
			name:     "simple issue",
			title:    "Crash on startup",
			number:   1234,
			expected: "crash-on-startup[1234].md",
		},
		{
			name:     "issue with special chars",
			title:    "Bug: Login fails!",
			number:   42,
			expected: "bug-login-fails[42].md",
		},
		{
			name:     "issue number 1",
			title:    "First issue",
			number:   1,
			expected: "first-issue[1].md",
		},
		{
			name:     "large issue number",
			title:    "Old issue",
			number:   999999,
			expected: "old-issue[999999].md",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := makeFilename(tc.title, tc.number)
			if result != tc.expected {
				t.Errorf("makeFilename(%q, %d) = %q, expected %q", tc.title, tc.number, result, tc.expected)
			}
		})
	}
}

func TestParseFilename(t *testing.T) {
	tests := []struct {
		name           string
		filename       string
		expectedNumber int
		expectedOK     bool
	}{
		{
			name:           "valid filename",
			filename:       "crash-on-startup[1234].md",
			expectedNumber: 1234,
			expectedOK:     true,
		},
		{
			name:           "simple filename",
			filename:       "bug[42].md",
			expectedNumber: 42,
			expectedOK:     true,
		},
		{
			name:           "filename with dashes",
			filename:       "fix-the-login-bug[999].md",
			expectedNumber: 999,
			expectedOK:     true,
		},
		{
			name:           "large number",
			filename:       "issue[999999].md",
			expectedNumber: 999999,
			expectedOK:     true,
		},
		{
			name:           "missing .md extension",
			filename:       "crash-on-startup[1234]",
			expectedNumber: 0,
			expectedOK:     false,
		},
		{
			name:           "missing brackets",
			filename:       "crash-on-startup-1234.md",
			expectedNumber: 0,
			expectedOK:     false,
		},
		{
			name:           "wrong extension",
			filename:       "crash-on-startup[1234].txt",
			expectedNumber: 0,
			expectedOK:     false,
		},
		{
			name:           "no number in brackets",
			filename:       "crash-on-startup[abc].md",
			expectedNumber: 0,
			expectedOK:     false,
		},
		{
			name:           "empty brackets",
			filename:       "crash-on-startup[].md",
			expectedNumber: 0,
			expectedOK:     false,
		},
		{
			name:           "empty filename",
			filename:       "",
			expectedNumber: 0,
			expectedOK:     false,
		},
		{
			name:           "just extension",
			filename:       ".md",
			expectedNumber: 0,
			expectedOK:     false,
		},
		{
			name:           "brackets in title",
			filename:       "fix-array[0]-bug[123].md",
			expectedNumber: 123,
			expectedOK:     true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			number, ok := parseFilename(tc.filename)
			if ok != tc.expectedOK {
				t.Errorf("parseFilename(%q) ok = %v, expected %v", tc.filename, ok, tc.expectedOK)
			}
			if number != tc.expectedNumber {
				t.Errorf("parseFilename(%q) number = %d, expected %d", tc.filename, number, tc.expectedNumber)
			}
		})
	}
}

func TestRoundTrip(t *testing.T) {
	// Test that makeFilename and parseFilename work together correctly
	tests := []struct {
		title  string
		number int
	}{
		{"Crash on startup", 1234},
		{"Bug: Login fails!", 42},
		{"Feature request", 1},
		{"Old issue", 999999},
	}

	for _, tc := range tests {
		t.Run(tc.title, func(t *testing.T) {
			filename := makeFilename(tc.title, tc.number)
			parsedNumber, ok := parseFilename(filename)
			if !ok {
				t.Errorf("parseFilename failed for generated filename %q", filename)
				return
			}
			if parsedNumber != tc.number {
				t.Errorf("Round trip failed: makeFilename(%q, %d) = %q, parseFilename returned %d",
					tc.title, tc.number, filename, parsedNumber)
			}
		})
	}
}
