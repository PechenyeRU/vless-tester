package core

import "strconv"

// setIf assigns m[k]=v only when v is non-empty, keeping configs minimal.
func setIf(m map[string]any, k, v string) {
	if v != "" {
		m[k] = v
	}
}

// orDefault returns v, or def when v is empty.
func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func atoiOrZero(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// isTruthy reports whether a query flag means "on" (1/true/yes).
func isTruthy(s string) bool {
	switch s {
	case "1", "true", "True", "yes":
		return true
	default:
		return false
	}
}
