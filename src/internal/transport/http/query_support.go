package httptransport

import (
	"strconv"
	"strings"
)

// ParsePositiveInt parses a positive integer and falls back on invalid input.
func ParsePositiveInt(s string, def int) int {
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || v <= 0 {
		return def
	}
	return v
}

// NormalizeSortParams constrains sort options to the supported API values.
func NormalizeSortParams(sortBy, sortOrder string) (string, string) {
	sortBy = strings.ToLower(strings.TrimSpace(sortBy))
	sortOrder = strings.ToLower(strings.TrimSpace(sortOrder))
	if sortBy != "name" && sortBy != "updated" {
		sortBy = "updated"
	}
	if sortOrder != "asc" && sortOrder != "desc" {
		if sortBy == "name" {
			sortOrder = "asc"
		} else {
			sortOrder = "desc"
		}
	}
	return sortBy, sortOrder
}
