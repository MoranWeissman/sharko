package api

import (
	"fmt"
	"net/http"
	"strconv"
)

const (
	defaultPage    = 1
	defaultPerPage = 20
	maxPerPage     = 100
)

type paginationParams struct {
	Page    int
	PerPage int
}

// parsePagination parses ?page= and ?per_page= from the query string.
// Defaults: page=1, per_page=20. per_page is clamped to [1, 100].
func parsePagination(r *http.Request) paginationParams {
	page := defaultPage
	perPage := defaultPerPage

	if v := r.URL.Query().Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 {
			page = n
		}
	}

	if v := r.URL.Query().Get("per_page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			if n < 1 {
				n = 1
			}
			if n > maxPerPage {
				n = maxPerPage
			}
			perPage = n
		}
	}

	return paginationParams{Page: page, PerPage: perPage}
}

// applyPagination slices a list to the requested page.
func applyPagination[T any](items []T, p paginationParams) []T {
	total := len(items)
	start := (p.Page - 1) * p.PerPage
	if start >= total {
		return []T{}
	}
	end := start + p.PerPage
	if end > total {
		end = total
	}
	return items[start:end]
}

// setPaginationHeaders sets X-Total-Count, X-Page, and X-Per-Page response headers.
func setPaginationHeaders(w http.ResponseWriter, total int, p paginationParams) {
	w.Header().Set("X-Total-Count", fmt.Sprintf("%d", total))
	w.Header().Set("X-Page", fmt.Sprintf("%d", p.Page))
	w.Header().Set("X-Per-Page", fmt.Sprintf("%d", p.PerPage))
}
