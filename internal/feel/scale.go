package feel

import (
	"math/big"
	"runtime"
	"strings"
	"sync"
	"weak"
)

// Numbers are *big.Rat, which normalize: 12.50 and 12.5 are one value. FEEL
// numbers (Camunda's are Java BigDecimals) also keep a scale: string(12.50)
// is "12.50", string(1.10 + 1) is "2.10", string(1.5 * 2) is "3.0". The
// scale of a number that has more decimals than its value needs rides
// beside it here, keyed weakly by its *big.Rat (an entry goes with its
// number), so everything else that handles numbers stays as it is.
var scales sync.Map // weak.Pointer[big.Rat] -> int

// withScale records that r has scale decimals, when its value needs fewer.
func withScale(r *big.Rat, scale int) *big.Rat {
	if natural := naturalScale(r); natural < 0 || scale <= natural {
		return r
	}
	key := weak.Make(r)
	scales.Store(key, scale)
	runtime.AddCleanup(r, func(k weak.Pointer[big.Rat]) { scales.Delete(k) }, key)
	return r
}

// scaleOf is r's scale: the one recorded for it, or the decimals its value
// needs; -1 for a value with no finite decimal form (1/3).
func scaleOf(r *big.Rat) int {
	if s, ok := scales.Load(weak.Make(r)); ok {
		return s.(int)
	}
	return naturalScale(r)
}

// naturalScale is how many decimals r's value needs, -1 if it has no
// finite decimal form.
func naturalScale(r *big.Rat) int {
	d := new(big.Int).Set(r.Denom())
	two, five := big.NewInt(2), big.NewInt(5)
	twos, fives := 0, 0
	m := new(big.Int)
	for d.Cmp(big.NewInt(1)) != 0 {
		if m.Mod(d, two).Sign() == 0 {
			d.Quo(d, two)
			twos++
		} else if m.Mod(d, five).Sign() == 0 {
			d.Quo(d, five)
			fives++
		} else {
			return -1
		}
	}
	return max(twos, fives)
}

// literalScale is the number of decimals a number literal is written with.
func literalScale(text string) int {
	if strings.ContainsAny(text, "eE") {
		return 0
	}
	if i := strings.IndexByte(text, '.'); i >= 0 {
		return len(text) - i - 1
	}
	return 0
}

// scaleArithmetic gives result the scale BigDecimal would: the larger of
// the operands' for + and -, their sum for *.
func scaleArithmetic(op string, a, b, result *big.Rat) *big.Rat {
	sa, sb := scaleOf(a), scaleOf(b)
	if sa < 0 || sb < 0 {
		return result
	}
	switch op {
	case "+", "-":
		return withScale(result, max(sa, sb))
	case "*":
		return withScale(result, sa+sb)
	}
	return result
}
