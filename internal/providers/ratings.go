package providers

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// NormalizeScale maps value on [0, max] to an integer 0–100 score.
func NormalizeScale(value, max float64) (int, error) {
	if max <= 0 {
		return 0, fmt.Errorf("rating scale max %v must be positive", max)
	}
	if value < 0 || value > max {
		return 0, fmt.Errorf("rating %v out of range [0, %v]", value, max)
	}
	return int(math.Round(value / max * 100)), nil
}

// ParseDisplay parses rating strings as OMDb renders them — "7.9/10",
// "85%", "74/100" — and returns the normalized 0–100 score.
func ParseDisplay(display string) (int, error) {
	s := strings.TrimSpace(display)
	if strings.HasSuffix(s, "%") {
		v, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
		if err != nil {
			return 0, fmt.Errorf("parse rating %q: %w", display, err)
		}
		return NormalizeScale(v, 100)
	}
	num, den, ok := strings.Cut(s, "/")
	if !ok {
		return 0, fmt.Errorf("parse rating %q: unrecognized format", display)
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(num), 64)
	if err != nil {
		return 0, fmt.Errorf("parse rating %q: %w", display, err)
	}
	max, err := strconv.ParseFloat(strings.TrimSpace(den), 64)
	if err != nil {
		return 0, fmt.Errorf("parse rating %q: %w", display, err)
	}
	return NormalizeScale(v, max)
}
