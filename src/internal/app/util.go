package app

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

func asString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case fmt.Stringer:
		return t.String()
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", t)
	}
}

func asInt(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case *int:
		if t == nil {
			return 0
		}
		return *t
	case int64:
		return int(t)
	case *int64:
		if t == nil {
			return 0
		}
		return int(*t)
	case float64:
		return int(t)
	case *float64:
		if t == nil {
			return 0
		}
		return int(*t)
	case jsonNumber:
		i, _ := t.Int64()
		return int(i)
	case string:
		i, _ := strconv.Atoi(t)
		return i
	default:
		return 0
	}
}

type jsonNumber interface{ Int64() (int64, error) }

func asIntPtr(v any) *int {
	if v == nil {
		return nil
	}
	i := asInt(v)
	return &i
}

func asFloat(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case string:
		f, _ := strconv.ParseFloat(t, 64)
		return f
	default:
		return 0
	}
}

func asBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return truthy(t)
	case int:
		return t != 0
	case int64:
		return t != 0
	case float64:
		return t != 0
	default:
		return false
	}
}

func fallback(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}

func truthy(v string) bool {
	s := strings.ToLower(strings.TrimSpace(v))
	return s == "1" || s == "true" || s == "yes" || s == "on"
}

func asStringSlice(v any) []string {
	switch t := v.(type) {
	case []string:
		out := make([]string, 0, len(t))
		for _, s := range t {
			if strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			s := strings.TrimSpace(asString(e))
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func uniqueStringsKeepOrder(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

var tagNameRe = regexp.MustCompile(`^[\p{L}\p{N}_]+$`)

func isValidTagName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	if strings.Contains(name, " ") {
		return false
	}
	return tagNameRe.MatchString(name)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func mustEnsureDirs(dirs ...string) {
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			panic(fmt.Sprintf("mkdir failed: %s: %v", d, err))
		}
	}
}
