package app

import (
	"fmt"
	"os"
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

func envString(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return i
}
