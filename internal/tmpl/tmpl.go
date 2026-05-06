// Package tmpl provides a tiny placeholder substitution helper used to plumb
// values like the configured app port through user-controlled command lines
// and snippet templates.
package tmpl

import (
	"strconv"
	"strings"
)

// Vars holds the values available for substitution.
type Vars struct {
	Port     int
	RepoPath string
	Name     string
}

// Apply substitutes {port}, {repo_path} and {name} in s. {port} is omitted
// (replaced with empty string) if Port <= 0.
func (v Vars) Apply(s string) string {
	port := ""
	if v.Port > 0 {
		port = strconv.Itoa(v.Port)
	}
	r := strings.NewReplacer(
		"{port}", port,
		"{repo_path}", v.RepoPath,
		"{name}", v.Name,
	)
	return r.Replace(s)
}

// ApplyAll runs Apply over each element of args and returns a new slice.
func (v Vars) ApplyAll(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = v.Apply(a)
	}
	return out
}

// HasPort reports whether any element references {port}.
func HasPort(args []string) bool {
	for _, a := range args {
		if strings.Contains(a, "{port}") {
			return true
		}
	}
	return false
}
