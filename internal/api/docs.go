package api

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var docsDir = findDocsDir()

func findDocsDir() string {
	// Try common locations
	for _, dir := range []string{
		"docs/user-guide",
		"/app/docs/user-guide",
	} {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	return "docs/user-guide"
}

type docEntry struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
	Order int    `json:"order"`
}

func (s *Server) handleDocsList(w http.ResponseWriter, _ *http.Request) {
	entries, err := os.ReadDir(docsDir)
	if err != nil {
		writeJSON(w, http.StatusOK, []docEntry{})
		return
	}

	var docs []docEntry
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		// Extract order from prefix like "01-overview"
		order := 0
		if len(name) > 2 && name[2] == '-' {
			for i := 0; i < 2; i++ {
				if name[i] >= '0' && name[i] <= '9' {
					order = order*10 + int(name[i]-'0')
				}
			}
		}
		// Extract title from first # heading in the file
		title := slugToTitle(name)
		data, readErr := os.ReadFile(filepath.Join(docsDir, e.Name()))
		if readErr == nil {
			if t := extractTitle(string(data)); t != "" {
				title = t
			}
		}
		docs = append(docs, docEntry{Slug: name, Title: title, Order: order})
	}

	sort.Slice(docs, func(i, j int) bool { return docs[i].Order < docs[j].Order })
	writeJSON(w, http.StatusOK, docs)
}

func (s *Server) handleDocsGet(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" {
		writeError(w, http.StatusBadRequest, "slug is required")
		return
	}
	// Sanitize: only allow alphanumeric, hyphens, underscores
	safe := regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	if !safe.MatchString(slug) {
		writeError(w, http.StatusBadRequest, "invalid slug")
		return
	}

	data, err := os.ReadFile(filepath.Join(docsDir, slug+".md"))
	if err != nil {
		writeError(w, http.StatusNotFound, "document not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"slug":    slug,
		"content": string(data),
	})
}

func extractTitle(content string) string {
	for _, line := range strings.SplitN(content, "\n", 5) {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimPrefix(line, "# ")
		}
	}
	return ""
}

func slugToTitle(slug string) string {
	// Remove numeric prefix like "01-"
	if len(slug) > 3 && slug[2] == '-' {
		slug = slug[3:]
	}
	return strings.ReplaceAll(slug, "-", " ")
}
