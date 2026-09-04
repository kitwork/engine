package relational

import (
	"cmp"
	"fmt"
	"math"
	"math/big"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

// Avoid converting the integer to float: adjacent BIGINTs above 2^53 can
// collapse to the same float. The mixed case compares against truncation.
func compareRuntimeNumbers(left, right any) (int, bool) {
	li, lint := runtimeIntegerValue(left)
	ri, rint := runtimeIntegerValue(right)
	if lint && rint {
		return cmp.Compare(li, ri), true
	}
	leftDecimal, leftExact, leftErr := runtimeDecimalNumber(left)
	rightDecimal, rightExact, rightErr := runtimeDecimalNumber(right)
	_, leftMarked := left.(exactDecimal)
	_, rightMarked := right.(exactDecimal)
	if leftErr == nil && rightErr == nil && leftExact && rightExact && (leftMarked || rightMarked) {
		leftText, leftTextErr := leftDecimal.text()
		rightText, rightTextErr := rightDecimal.text()
		if leftTextErr == nil && rightTextErr == nil {
			return compareCanonicalDecimals(leftText, rightText), true
		}
	}
	if leftMarked {
		if rightFloat, ok := runtimeFloatValue(right); ok {
			leftRational, err := leftDecimal.rational()
			rightRational := new(big.Rat).SetFloat64(rightFloat)
			if err == nil && rightRational != nil {
				return leftRational.Cmp(rightRational), true
			}
		}
	}
	if rightMarked {
		if leftFloat, ok := runtimeFloatValue(left); ok {
			rightRational, err := rightDecimal.rational()
			leftRational := new(big.Rat).SetFloat64(leftFloat)
			if err == nil && leftRational != nil {
				return leftRational.Cmp(rightRational), true
			}
		}
	}
	lf, lnum := runtimeNumericValue(left)
	rf, rnum := runtimeNumericValue(right)
	if !lnum || !rnum {
		return 0, false
	}
	if lint {
		return compareIntegerFloat(li, rf), true
	}
	if rint {
		return -compareIntegerFloat(ri, lf), true
	}
	return cmp.Compare(lf, rf), true
}

func runtimeFloatValue(value any) (float64, bool) {
	switch current := value.(type) {
	case float32:
		return float64(current), true
	case float64:
		return current, !math.IsNaN(current) && !math.IsInf(current, 0)
	default:
		return 0, false
	}
}

func compareIntegerFloat(integer int64, number float64) int {
	if number >= 0x1p63 {
		return -1
	}
	if number < -0x1p63 {
		return 1
	}
	truncated := int64(number)
	if comparison := cmp.Compare(integer, truncated); comparison != 0 {
		return comparison
	}
	return cmp.Compare(float64(truncated), number)
}

func exactIntegerAggregate(field *kitdbsql.Field, function string) bool {
	return field != nil && exactIntegerFieldKind(field.Kind) && (function == "sum" || function == "avg")
}

func integerAggregateResultKind(field *kitdbsql.Field, function string) string {
	if !exactIntegerAggregate(field, function) {
		return "float"
	}
	// PostgreSQL widens SUM(int2/int4) to int8. BIGINT SUM and every integer
	// AVG use the exact decimal accumulator already required by KitDB's int8.
	if function == "sum" && field.Kind != "bigint" {
		return "bigint"
	}
	return "decimal"
}

type integerSum struct {
	small int64
	wide  *big.Int
}

func (sum *integerSum) add(value int64) {
	if sum.wide == nil && !(value > 0 && sum.small > math.MaxInt64-value || value < 0 && sum.small < math.MinInt64-value) {
		sum.small += value
		return
	}
	if sum.wide == nil {
		sum.wide = big.NewInt(sum.small)
	}
	var operand big.Int
	operand.SetInt64(value)
	sum.wide.Add(sum.wide, &operand)
}

func (sum *integerSum) addSigned128(high int64, low uint64) {
	if high == 0 && low <= math.MaxInt64 || high == -1 && low >= 1<<63 {
		sum.add(int64(low))
		return
	}
	if sum.wide == nil {
		sum.wide = big.NewInt(sum.small)
	}
	var operand, lower big.Int
	operand.SetInt64(high)
	operand.Lsh(&operand, 64)
	lower.SetUint64(low)
	operand.Add(&operand, &lower)
	sum.wide.Add(sum.wide, &operand)
}

// SUM is exact. AVG uses a documented 16 fractional decimal digits with
// rounding half away from zero; it never rounds its inputs through float64.
func (sum *integerSum) result(count int64, function string) string {
	integer := sum.wide
	var small big.Int
	if integer == nil {
		integer = small.SetInt64(sum.small)
	}
	if function == "sum" {
		return integer.String()
	}
	var denominator big.Int
	denominator.SetInt64(count)
	var average big.Rat
	average.SetFrac(integer, &denominator)
	text, _ := canonicalDecimal(average.FloatString(16))
	return text
}

func (sum *integerSum) int64Result() (int64, error) {
	if sum.wide == nil {
		return sum.small, nil
	}
	if !sum.wide.IsInt64() {
		return 0, fmt.Errorf("kitdb SQL: BIGINT aggregate overflow")
	}
	return sum.wide.Int64(), nil
}

func (sum *integerSum) aggregateResult(count int64, function string, field *kitdbsql.Field) (any, error) {
	if function == "sum" && field != nil && field.Kind != "bigint" {
		return sum.int64Result()
	}
	return sum.result(count, function), nil
}
