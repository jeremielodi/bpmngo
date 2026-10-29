package simulation

import (
	"fmt"
	"math"
	"math/rand"
	"strings"
)

func (d Distribution) Expected() (float64, error) {
	switch strings.ToLower(d.Type) {
	case "", "fixed", "constant":
		return nonNegative(d.Value), nil
	case "normal", "truncated-normal":
		if d.StandardDeviation < 0 {
			return 0, fmt.Errorf("normal standard deviation must be >= 0")
		}
		return nonNegative(d.Mean), nil
	case "exponential":
		if d.Mean <= 0 {
			return 0, fmt.Errorf("exponential mean must be > 0")
		}
		return d.Mean, nil
	case "uniform":
		if d.Max < d.Min {
			return 0, fmt.Errorf("uniform distribution max must be >= min")
		}
		return nonNegative((d.Min + d.Max) / 2), nil
	case "triangular":
		if d.Max < d.Min || d.Mode < d.Min || d.Mode > d.Max {
			return 0, fmt.Errorf("invalid triangular distribution bounds")
		}
		return nonNegative((d.Min + d.Mode + d.Max) / 3), nil
	default:
		return 0, fmt.Errorf("unsupported distribution %q", d.Type)
	}
}

func (d Distribution) Sample(rng *rand.Rand) (float64, error) {
	switch strings.ToLower(d.Type) {
	case "", "fixed", "constant":
		return nonNegative(d.Value), nil
	case "normal", "truncated-normal":
		if d.StandardDeviation < 0 {
			return 0, fmt.Errorf("normal standard deviation must be >= 0")
		}
		for range 16 {
			v := rng.NormFloat64()*d.StandardDeviation + d.Mean
			if v >= 0 {
				return v, nil
			}
		}
		return 0, nil
	case "exponential":
		if d.Mean <= 0 {
			return 0, fmt.Errorf("exponential mean must be > 0")
		}
		return rng.ExpFloat64() * d.Mean, nil
	case "uniform":
		if d.Max < d.Min {
			return 0, fmt.Errorf("uniform distribution max must be >= min")
		}
		return nonNegative(d.Min + rng.Float64()*(d.Max-d.Min)), nil
	case "triangular":
		if d.Max < d.Min || d.Mode < d.Min || d.Mode > d.Max {
			return 0, fmt.Errorf("invalid triangular distribution bounds")
		}
		if d.Max == d.Min {
			return nonNegative(d.Min), nil
		}
		u := rng.Float64()
		f := (d.Mode - d.Min) / (d.Max - d.Min)
		if u < f {
			return nonNegative(d.Min + math.Sqrt(u*(d.Max-d.Min)*(d.Mode-d.Min))), nil
		}
		return nonNegative(d.Max - math.Sqrt((1-u)*(d.Max-d.Min)*(d.Max-d.Mode))), nil
	default:
		return 0, fmt.Errorf("unsupported distribution %q", d.Type)
	}
}

func nonNegative(value float64) float64 {
	if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return value
}
