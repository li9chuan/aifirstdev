package rdb

import (
	"fmt"
	"sort"
	"strings"
)

// LoadReport records recovered object counts by kind.
type LoadReport struct {
	Counts map[string]int
	Errors []error
}

// NewLoadReport creates an empty initialized load report.
func NewLoadReport() LoadReport {
	return LoadReport{Counts: make(map[string]int)}
}

// Add increases the recovered count for one object kind.
func (r *LoadReport) Add(kind string, count int) {
	if r.Counts == nil {
		r.Counts = make(map[string]int)
	}
	r.Counts[kind] += count
}

// AddError records a non-nil recovery error.
func (r *LoadReport) AddError(err error) {
	if err != nil {
		r.Errors = append(r.Errors, err)
	}
}

// String formats counts in stable key order and appends the error count when present.
func (r LoadReport) String() string {
	names := make([]string, 0, len(r.Counts))
	for name := range r.Counts {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names)+1)
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s=%d", name, r.Counts[name]))
	}
	if len(r.Errors) > 0 {
		parts = append(parts, fmt.Sprintf("errors=%d", len(r.Errors)))
	}
	return strings.Join(parts, " ")
}
