package ai

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// WebSearch performs a web search using DuckDuckGo HTML search (no API key needed).
// It returns formatted results as a string, limited to maxResults entries.
func WebSearch(ctx context.Context, query string, maxResults int) (string, error) {
	if query == "" {
		return "Please specify a search query.", nil
	}
	if maxResults <= 0 {
		maxResults = 5
	}

	searchURL := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(query)

	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return "", fmt.Errorf("creating search request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; ArgoCD-Addons-Platform/1.0)")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("web search request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading search response: %w", err)
	}

	html := string(body)
	results := parseSearchResults(html, maxResults)

	if len(results) == 0 {
		return fmt.Sprintf("No search results found for: %s", query), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Search results for: %s\n\n", query)
	for i, r := range results {
		fmt.Fprintf(&sb, "%d. %s\n   %s\n\n", i+1, r.title, r.snippet)
	}
	return sb.String(), nil
}

type searchResult struct {
	title   string
	snippet string
}

// parseSearchResults extracts titles and snippets from DuckDuckGo HTML search results.
func parseSearchResults(html string, maxResults int) []searchResult {
	var results []searchResult

	// Look for result titles (class="result__title") and snippets (class="result__snippet")
	remaining := html
	for len(results) < maxResults {
		// Find next result title
		titleIdx := strings.Index(remaining, `class="result__title"`)
		if titleIdx == -1 {
			break
		}
		remaining = remaining[titleIdx:]

		// Extract title text from the anchor tag within the title div
		title := extractTagText(remaining, "a")
		if title == "" {
			// Try to advance past this result
			if len(remaining) > 30 {
				remaining = remaining[30:]
			} else {
				break
			}
			continue
		}

		// Find the snippet for this result
		snippet := ""
		snippetIdx := strings.Index(remaining, `class="result__snippet"`)
		if snippetIdx != -1 {
			snippetHTML := remaining[snippetIdx:]
			snippet = extractInnerText(snippetHTML)
		}

		title = cleanText(title)
		snippet = cleanText(snippet)

		if title != "" {
			results = append(results, searchResult{title: title, snippet: snippet})
		}

		// Advance past this result
		nextIdx := strings.Index(remaining[30:], `class="result__title"`)
		if nextIdx == -1 {
			break
		}
		remaining = remaining[30+nextIdx:]
	}

	return results
}

// extractTagText extracts text content from the first occurrence of the given tag.
func extractTagText(html, tag string) string {
	openTag := "<" + tag
	idx := strings.Index(html, openTag)
	if idx == -1 {
		return ""
	}

	// Find the end of the opening tag
	closeAngle := strings.Index(html[idx:], ">")
	if closeAngle == -1 {
		return ""
	}
	start := idx + closeAngle + 1

	// Find the closing tag
	closeTag := "</" + tag + ">"
	end := strings.Index(html[start:], closeTag)
	if end == -1 {
		return ""
	}

	return stripTags(html[start : start+end])
}

// extractInnerText extracts text after the closing > of the current tag until the next closing tag.
func extractInnerText(html string) string {
	// Find the end of the opening tag
	closeAngle := strings.Index(html, ">")
	if closeAngle == -1 {
		return ""
	}
	start := closeAngle + 1

	// Find a reasonable end point (next closing div/span or end of content)
	endMarkers := []string{"</a>", "</span>", "</div>"}
	end := len(html)
	for _, marker := range endMarkers {
		idx := strings.Index(html[start:], marker)
		if idx != -1 && start+idx < end {
			end = start + idx
		}
	}

	if end <= start {
		return ""
	}

	return stripTags(html[start:end])
}

// stripTags removes HTML tags from a string.
func stripTags(s string) string {
	var result strings.Builder
	inTag := false
	for _, r := range s {
		if r == '<' {
			inTag = true
			continue
		}
		if r == '>' {
			inTag = false
			continue
		}
		if !inTag {
			result.WriteRune(r)
		}
	}
	return result.String()
}

// cleanText trims whitespace and collapses multiple spaces.
func cleanText(s string) string {
	s = strings.TrimSpace(s)
	// Collapse whitespace
	parts := strings.Fields(s)
	return strings.Join(parts, " ")
}
