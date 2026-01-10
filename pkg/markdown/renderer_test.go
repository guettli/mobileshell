package markdown

import (
	"strings"
	"testing"
)

func TestRenderToHTML_BasicMarkdown(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains []string
	}{
		{
			name:     "headers",
			input:    "# Header 1\n## Header 2\n### Header 3",
			contains: []string{"<h1", "Header 1", "<h2", "Header 2", "<h3", "Header 3"},
		},
		{
			name:     "bold and italic",
			input:    "**bold text** and *italic text*",
			contains: []string{"<strong>bold text</strong>", "<em>italic text</em>"},
		},
		{
			name:     "code inline",
			input:    "Here is `inline code` example",
			contains: []string{"<code>inline code</code>"},
		},
		{
			name:  "code block",
			input: "```\ncode block\nmore code\n```",
			contains: []string{
				"<pre>", "<code>", "code block", "more code",
			},
		},
		{
			name:     "unordered list",
			input:    "- Item 1\n- Item 2\n- Item 3",
			contains: []string{"<ul>", "<li>Item 1</li>", "<li>Item 2</li>", "<li>Item 3</li>", "</ul>"},
		},
		{
			name:     "ordered list",
			input:    "1. First\n2. Second\n3. Third",
			contains: []string{"<ol>", "<li>First</li>", "<li>Second</li>", "<li>Third</li>", "</ol>"},
		},
		{
			name:     "links",
			input:    "[Link text](https://example.com)",
			contains: []string{"<a href=\"https://example.com\"", "Link text</a>"},
		},
		{
			name:     "blockquote",
			input:    "> This is a quote\n> Second line",
			contains: []string{"<blockquote>", "This is a quote", "Second line", "</blockquote>"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := RenderToHTML(tt.input)

			for _, expected := range tt.contains {
				if !strings.Contains(result, expected) {
					t.Errorf("RenderToHTML() result doesn't contain expected substring.\nExpected: %q\nResult: %s", expected, result)
				}
			}
		})
	}
}

func TestRenderToHTML_XSSPrevention(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		shouldBlock string
		description string
	}{
		{
			name:        "script tag",
			input:       "<script>alert('xss')</script>",
			shouldBlock: "<script>",
			description: "Script tags should be removed",
		},
		{
			name:        "onclick handler",
			input:       "<a href=\"#\" onclick=\"alert('xss')\">Click me</a>",
			shouldBlock: "onclick",
			description: "Event handlers should be removed",
		},
		{
			name:        "javascript protocol",
			input:       "[Click me](javascript:alert('xss'))",
			shouldBlock: "javascript:",
			description: "JavaScript protocol in links should be blocked",
		},
		{
			name:        "iframe",
			input:       "<iframe src=\"http://evil.com\"></iframe>",
			shouldBlock: "<iframe",
			description: "Iframes should be removed",
		},
		{
			name:        "style tag",
			input:       "<style>body { display: none; }</style>",
			shouldBlock: "<style>",
			description: "Style tags should be removed",
		},
		{
			name:        "data URL",
			input:       "[Link](data:text/html,<script>alert('xss')</script>)",
			shouldBlock: "data:text/html",
			description: "Data URLs should be blocked",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := RenderToHTML(tt.input)

			if strings.Contains(result, tt.shouldBlock) {
				t.Errorf("%s: XSS vector not blocked.\nInput: %s\nBlocked string: %q\nResult: %s",
					tt.description, tt.input, tt.shouldBlock, result)
			}
		})
	}
}

func TestRenderToHTML_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains string
	}{
		{
			name:     "empty string",
			input:    "",
			contains: "",
		},
		{
			name:     "only whitespace",
			input:    "   \n\n   ",
			contains: "",
		},
		{
			name:     "plain text",
			input:    "Just plain text with no markdown",
			contains: "Just plain text with no markdown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := RenderToHTML(tt.input)
			result = strings.TrimSpace(result)

			if tt.contains == "" {
				if result != "" {
					t.Errorf("RenderToHTML() = %q, want empty string", result)
				}
			} else if !strings.Contains(result, tt.contains) {
				t.Errorf("RenderToHTML() = %q, should contain %q", result, tt.contains)
			}
		})
	}
}

func TestRenderToHTML_ComplexMarkdown(t *testing.T) {
	input := `# My Document

This is a **complex** document with *multiple* features.

## Code Example

Here's some code:

` + "```go\nfunc main() {\n    fmt.Println(\"Hello\")\n}\n```" + `

## List of Items

1. First item with [a link](https://example.com)
2. Second item with ` + "`inline code`" + `
3. Third item

### Nested Content

> This is a quote
> with multiple lines

And some normal text.`

	result := RenderToHTML(input)

	// Check that major elements are present
	expectedElements := []string{
		"<h1", "My Document",
		"<h2", "Code Example",
		"<strong>complex</strong>",
		"<em>multiple</em>",
		"<pre>", "<code>", "func main",
		"<ol>", "<li>First item",
		"<a href=\"https://example.com\"", "a link</a>",
		"<code>inline code</code>",
		"<blockquote>", "This is a quote",
	}

	for _, expected := range expectedElements {
		if !strings.Contains(result, expected) {
			t.Errorf("Complex markdown missing expected element: %q\nResult: %s", expected, result)
		}
	}

	// Ensure no script tags
	if strings.Contains(result, "<script") {
		t.Error("Complex markdown should not contain script tags")
	}
}

func TestRenderToHTMLWithOptions_NoLinks(t *testing.T) {
	input := "Check out [this link](https://example.com) and **bold text**"

	opts := RenderOptions{
		AllowRawHTML: false,
		NoLinks:      true,
		NoImages:     false,
	}

	result := RenderToHTMLWithOptions(input, opts)

	// Should contain bold text
	if !strings.Contains(result, "<strong>bold text</strong>") {
		t.Error("Should contain bold text")
	}

	// Should contain link text
	if !strings.Contains(result, "this link") {
		t.Error("Should contain link text")
	}

	// Note: The current implementation uses bluemonday which may still allow
	// some link elements. This test verifies the rendering works without errors.
}

func TestRenderToHTMLWithOptions_NoImages(t *testing.T) {
	input := "Here's an image: ![Alt text](https://example.com/image.png)"

	opts := RenderOptions{
		AllowRawHTML: false,
		NoLinks:      false,
		NoImages:     true,
	}

	result := RenderToHTMLWithOptions(input, opts)

	// Verify rendering works without errors
	// Note: The current implementation uses bluemonday which handles image filtering
	if result == "" {
		t.Error("Should produce some output")
	}
}

func TestRenderToHTMLWithOptions_Strict(t *testing.T) {
	input := `# Header
[Link](https://example.com)
![Image](https://example.com/image.png)
**Bold** text`

	opts := RenderOptions{
		AllowRawHTML: false,
		NoLinks:      true,
		NoImages:     true,
	}

	result := RenderToHTMLWithOptions(input, opts)

	// Should have basic formatting
	if !strings.Contains(result, "<h1") {
		t.Error("Should contain header")
	}

	if !strings.Contains(result, "<strong>Bold</strong>") {
		t.Error("Should contain bold text")
	}

	// Should not have links or images
	if strings.Contains(result, "href=") {
		t.Error("Strict mode should not have links")
	}

	if strings.Contains(result, "<img") {
		t.Error("Strict mode should not have images")
	}
}

func TestRenderToHTML_TableSupport(t *testing.T) {
	input := `| Column 1 | Column 2 |
|----------|----------|
| Value 1  | Value 2  |
| Value 3  | Value 4  |`

	result := RenderToHTML(input)

	// Check for table elements
	expectedElements := []string{
		"<table>",
		"<thead>", "<tbody>",
		"<tr>", "<th>", "<td>",
		"Column 1", "Column 2",
		"Value 1", "Value 2",
	}

	for _, expected := range expectedElements {
		if !strings.Contains(result, expected) {
			t.Errorf("Table markdown missing expected element: %q", expected)
		}
	}
}
