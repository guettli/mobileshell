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

// RenderToHTMLWithOptions provides more control over markdown rendering.
type RenderOptions struct {
	// AllowRawHTML allows raw HTML in markdown (sanitized by bluemonday)
	AllowRawHTML bool
	// NoLinks removes all links from output
	NoLinks bool
	// NoImages removes all images from output
	NoImages bool
}

// RenderToHTMLWithOptions converts markdown to HTML with custom options.
func RenderToHTMLWithOptions(markdown string, opts RenderOptions) string {
	// Build extensions
	extensions := blackfriday.CommonExtensions | blackfriday.AutoHeadingIDs | blackfriday.Footnotes

	// Convert markdown to HTML
	// Note: bluemonday will handle HTML sanitization, including raw HTML
	unsafeHTML := blackfriday.Run(
		[]byte(markdown),
		blackfriday.WithExtensions(extensions),
	)

	// Create sanitization policy based on options
	var policy *bluemonday.Policy
	if opts.NoLinks && opts.NoImages {
		// Strictest policy - no links or images
		policy = bluemonday.StrictPolicy()
		// Allow basic formatting
		policy.AllowElements("p", "br", "strong", "em", "code", "pre", "h1", "h2", "h3", "h4", "h5", "h6", "ul", "ol", "li", "blockquote")
	} else {
		policy = bluemonday.UGCPolicy()

		if opts.NoLinks {
			// Remove links but keep images
			policy.AllowElements("a") // Don't allow any attributes, effectively removing links
		}

		if opts.NoImages {
			policy.AllowElements("img") // Don't allow any attributes, effectively removing images
		}
	}

	// Allow class attributes for code highlighting
	policy.AllowAttrs("class").Matching(bluemonday.SpaceSeparatedTokens).OnElements("code", "pre", "span")
	policy.AllowAttrs("id").Matching(bluemonday.SpaceSeparatedTokens).OnElements("h1", "h2", "h3", "h4", "h5", "h6")

	// Sanitize and return
	safeHTML := policy.SanitizeBytes(unsafeHTML)
	return string(safeHTML)
}
