package markdown

import (
	"github.com/microcosm-cc/bluemonday"
	"github.com/russross/blackfriday/v2"
)

// RenderToHTML converts markdown text to sanitized HTML.
// It uses blackfriday for markdown parsing and bluemonday for HTML sanitization
// to prevent XSS attacks while preserving safe formatting.
func RenderToHTML(markdown string) string {
	// Convert markdown to HTML using blackfriday
	unsafeHTML := blackfriday.Run(
		[]byte(markdown),
		blackfriday.WithExtensions(
			blackfriday.CommonExtensions|
				blackfriday.AutoHeadingIDs|
				blackfriday.Footnotes,
		),
	)

	// Sanitize HTML using bluemonday
	// Use UGCPolicy which allows user-generated content with safe HTML tags
	policy := bluemonday.UGCPolicy()

	// Allow additional safe attributes for better formatting
	policy.AllowAttrs("class").Matching(bluemonday.SpaceSeparatedTokens).OnElements("code", "pre", "span")
	policy.AllowAttrs("id").Matching(bluemonday.SpaceSeparatedTokens).OnElements("h1", "h2", "h3", "h4", "h5", "h6")

	// Sanitize and return as string
	safeHTML := policy.SanitizeBytes(unsafeHTML)
	return string(safeHTML)
}
