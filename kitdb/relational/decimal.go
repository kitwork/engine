package relational

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

const decimalDivisionScale = 16

const maximumDecimalGroupStateBytes = 16 << 20

// exactDecimal marks a runtime SQL value that must not pass through float64.
// Durable rows and public results remain canonical strings.
type exactDecimal string

type decimalNumber struct {
	coefficient big.Int
	scale       int
}

func parseDecimalNumber(source string) (decimalNumber, error) {
	canonical, err := canonicalDecimal(source)
	if err != nil {
		return decimalNumber{}, err
	}
	negative := strings.HasPrefix(canonical, "-")
	if negative {
		canonical = canonical[1:]
	}
	integer, fraction := decimalParts(canonical)
	digits := integer + fraction
	var coefficient big.Int
	if _, ok := coefficient.SetString(digits, 10); !ok {
		return decimalNumber{}, fmt.Errorf("invalid decimal coefficient")
	}
	if negative {
		coefficient.Neg(&coefficient)
	}
	return decimalNumber{coefficient: coefficient, scale: len(fraction)}, nil
}

func decimalFromInteger(value int64) decimalNumber {
	var coefficient big.Int
	coefficient.SetInt64(value)
	return decimalNumber{coefficient: coefficient}
}

func (number decimalNumber) text() (string, error) {
	negative := number.coefficient.Sign() < 0
	var absolute big.Int
	absolute.Abs(&number.coefficient)
	digits := absolute.String()
	if number.scale < 0 || number.scale > maximumDecimalText {
		return "", fmt.Errorf("decimal scale is out of range")
	}
	var text string
	switch {
	case number.scale == 0:
		text = digits
	case number.scale >= len(digits):
		text = "0." + strings.Repeat("0", number.scale-len(digits)) + digits
	default:
		position := len(digits) - number.scale
		text = digits[:position] + "." + digits[position:]
	}
	if negative && absolute.Sign() != 0 {
		text = "-" + text
	}
	canonical, err := canonicalDecimal(text)
	if err != nil {
		return "", fmt.Errorf("decimal result is out of range: %w", err)
	}
	return canonical, nil
}

func (number decimalNumber) rational() (*big.Rat, error) {
	denominator, err := decimalPower10(number.scale)
	if err != nil {
		return nil, err
	}
	return new(big.Rat).SetFrac(new(big.Int).Set(&number.coefficient), denominator), nil
}

func decimalPower10(exponent int) (*big.Int, error) {
	if exponent < 0 || exponent > maximumDecimalText {
		return nil, fmt.Errorf("decimal exponent is out of range")
	}
	var result, ten, power big.Int
	ten.SetInt64(10)
	power.SetInt64(int64(exponent))
	result.Exp(&ten, &power, nil)
	return &result, nil
}

func scaleDecimalCoefficient(number decimalNumber, scale int) (*big.Int, error) {
	if scale < number.scale {
		return nil, fmt.Errorf("decimal scale cannot be narrowed without rounding")
	}
	result := new(big.Int).Set(&number.coefficient)
	if scale == number.scale {
		return result, nil
	}
	power, err := decimalPower10(scale - number.scale)
	if err != nil {
		return nil, err
	}
	return result.Mul(result, power), nil
}

func addDecimalNumbers(left, right decimalNumber) (decimalNumber, error) {
	scale := max(left.scale, right.scale)
	leftCoefficient, err := scaleDecimalCoefficient(left, scale)
	if err != nil {
		return decimalNumber{}, err
	}
	rightCoefficient, err := scaleDecimalCoefficient(right, scale)
	if err != nil {
		return decimalNumber{}, err
	}
	var coefficient big.Int
	coefficient.Add(leftCoefficient, rightCoefficient)
	result := decimalNumber{coefficient: coefficient, scale: scale}
	if _, err := result.text(); err != nil {
		return decimalNumber{}, err
	}
	return result, nil
}

func subtractDecimalNumbers(left, right decimalNumber) (decimalNumber, error) {
	var negative big.Int
	negative.Neg(&right.coefficient)
	return addDecimalNumbers(left, decimalNumber{coefficient: negative, scale: right.scale})
}

func multiplyDecimalNumbers(left, right decimalNumber) (decimalNumber, error) {
	if left.scale > maximumDecimalText-right.scale {
		return decimalNumber{}, fmt.Errorf("decimal result scale is out of range")
	}
	var coefficient big.Int
	coefficient.Mul(&left.coefficient, &right.coefficient)
	result := decimalNumber{coefficient: coefficient, scale: left.scale + right.scale}
	if _, err := result.text(); err != nil {
		return decimalNumber{}, err
	}
	return result, nil
}

func divideDecimalNumbers(left, right decimalNumber, scale int) (decimalNumber, error) {
	if right.coefficient.Sign() == 0 {
		return decimalNumber{}, fmt.Errorf("division by zero")
	}
	if scale < 0 || scale > maximumDecimalText || right.scale > maximumDecimalText-scale {
		return decimalNumber{}, fmt.Errorf("decimal division scale is out of range")
	}
	numerator := new(big.Int).Set(&left.coefficient)
	power, err := decimalPower10(right.scale + scale)
	if err != nil {
		return decimalNumber{}, err
	}
	numerator.Mul(numerator, power)
	denominator := new(big.Int).Set(&right.coefficient)
	if left.scale != 0 {
		power, err = decimalPower10(left.scale)
		if err != nil {
			return decimalNumber{}, err
		}
		denominator.Mul(denominator, power)
	}
	var quotient, remainder big.Int
	quotient.QuoRem(numerator, denominator, &remainder)
	if remainder.Sign() != 0 {
		var twiceRemainder, absoluteDenominator big.Int
		twiceRemainder.Abs(&remainder)
		twiceRemainder.Lsh(&twiceRemainder, 1)
		absoluteDenominator.Abs(denominator)
		if twiceRemainder.Cmp(&absoluteDenominator) >= 0 {
			if numerator.Sign()*denominator.Sign() < 0 {
				quotient.Sub(&quotient, big.NewInt(1))
			} else {
				quotient.Add(&quotient, big.NewInt(1))
			}
		}
	}
	result := decimalNumber{coefficient: quotient, scale: scale}
	if _, err := result.text(); err != nil {
		return decimalNumber{}, err
	}
	return result, nil
}

func moduloDecimalNumbers(left, right decimalNumber) (decimalNumber, error) {
	if right.coefficient.Sign() == 0 {
		return decimalNumber{}, fmt.Errorf("division by zero")
	}
	scale := max(left.scale, right.scale)
	leftCoefficient, err := scaleDecimalCoefficient(left, scale)
	if err != nil {
		return decimalNumber{}, err
	}
	rightCoefficient, err := scaleDecimalCoefficient(right, scale)
	if err != nil {
		return decimalNumber{}, err
	}
	var coefficient big.Int
	coefficient.Rem(leftCoefficient, rightCoefficient)
	return decimalNumber{coefficient: coefficient, scale: scale}, nil
}

// roundDecimalNumber rounds half away from zero. A negative target scale
// rounds to powers of ten to the left of the decimal point.
func roundDecimalNumber(number decimalNumber, targetScale int) (decimalNumber, error) {
	if targetScale >= number.scale {
		return number, nil
	}
	difference := number.scale - targetScale
	divisor, err := decimalPower10(difference)
	if err != nil {
		return decimalNumber{}, err
	}
	var quotient, remainder big.Int
	quotient.QuoRem(&number.coefficient, divisor, &remainder)
	if remainder.Sign() != 0 {
		var twiceRemainder big.Int
		twiceRemainder.Abs(&remainder)
		twiceRemainder.Lsh(&twiceRemainder, 1)
		if twiceRemainder.Cmp(divisor) >= 0 {
			if number.coefficient.Sign() < 0 {
				quotient.Sub(&quotient, big.NewInt(1))
			} else {
				quotient.Add(&quotient, big.NewInt(1))
			}
		}
	}
	if targetScale < 0 {
		power, err := decimalPower10(-targetScale)
		if err != nil {
			return decimalNumber{}, err
		}
		quotient.Mul(&quotient, power)
		return decimalNumber{coefficient: quotient}, nil
	}
	return decimalNumber{coefficient: quotient, scale: targetScale}, nil
}

func coerceDecimal(field kitdbsql.Field, source string) (string, error) {
	number, err := parseDecimalNumber(source)
	if err != nil {
		return "", err
	}
	if field.Precision == 0 {
		return number.text()
	}
	number, err = roundDecimalNumber(number, field.Scale)
	if err != nil {
		return "", err
	}
	text, err := number.text()
	if err != nil {
		return "", err
	}
	unsigned := strings.TrimPrefix(text, "-")
	integer, fraction := decimalParts(unsigned)
	integerDigits := len(strings.TrimLeft(integer, "0"))
	if integerDigits > field.Precision-field.Scale || len(fraction) > field.Scale {
		return "", fmt.Errorf("value exceeds NUMERIC(%d,%d)", field.Precision, field.Scale)
	}
	return text, nil
}

func runtimeDecimalNumber(item any) (decimalNumber, bool, error) {
	switch current := item.(type) {
	case exactDecimal:
		number, err := parseDecimalNumber(string(current))
		return number, true, err
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		integer, err := integerValue(item)
		if err != nil {
			return decimalNumber{}, true, err
		}
		return decimalFromInteger(integer), true, nil
	default:
		return decimalNumber{}, false, nil
	}
}

func decimalRuntimeResult(number decimalNumber) (exactDecimal, error) {
	text, err := number.text()
	return exactDecimal(text), err
}

func castScalarValue(value any, expression *boundPredicate) (any, error) {
	if expression == nil || expression.castKind == "" {
		return nil, fmt.Errorf("kitdb SQL: CAST target is unavailable")
	}
	field := kitdbsql.Field{
		Name: "cast", Kind: expression.castKind,
		Precision: expression.precision, Scale: expression.scale,
		TimePrecision: expression.timePrecision, TextLength: expression.textLength,
		ExactUUID: expression.castKind == "uuid",
	}
	if expression.castKind == "text" || expression.castKind == "varchar" || expression.castKind == "char" {
		var text string
		switch current := value.(type) {
		case exactDecimal:
			text = string(current)
		case exactTemporal:
			text = current.text
		case string:
			text = current
		case []byte:
			text = string(current)
		default:
			text = fmt.Sprint(value)
		}
		if len(expression.arguments) == 1 && exactCharacterPredicate(expression.arguments[0]) {
			text = strings.TrimRight(text, " ")
		}
		result, err := coerceTextField(field, text, true)
		if err != nil {
			return nil, fmt.Errorf("kitdb SQL: CAST AS %s: %w", strings.ToUpper(expression.castKind), err)
		}
		return result, nil
	}
	if _, ok := value.(exactDecimal); ok && isIntegerExpressionKind(expression.castKind) {
		number, err := parseDecimalNumber(string(value.(exactDecimal)))
		if err != nil {
			return nil, fmt.Errorf("kitdb SQL: CAST: %w", err)
		}
		number, err = roundDecimalNumber(number, 0)
		if err != nil {
			return nil, fmt.Errorf("kitdb SQL: CAST: %w", err)
		}
		value, err = number.text()
		if err != nil {
			return nil, fmt.Errorf("kitdb SQL: CAST: %w", err)
		}
	}
	coerced, err := coerceField(field, value)
	if err != nil {
		return nil, fmt.Errorf("kitdb SQL: CAST AS %s: %w", strings.ToUpper(expression.castKind), err)
	}
	result := readField(field, coerced)
	if expression.castKind == "decimal" && result != nil {
		return exactDecimal(result.(string)), nil
	}
	if exactTemporalFieldKind(expression.castKind) && result != nil {
		return parseTemporal(expression.castKind, result.(string), expression.timePrecision)
	}
	return result, nil
}

func exactDecimalBinary(operator string, left, right any) (any, bool, error) {
	leftNumber, leftDecimal, err := runtimeDecimalNumber(left)
	if err != nil {
		return nil, true, err
	}
	rightNumber, rightDecimal, err := runtimeDecimalNumber(right)
	if err != nil {
		return nil, true, err
	}
	if !leftDecimal || !rightDecimal {
		return nil, false, nil
	}
	// Preserve the existing exact integer path unless an operand is decimal.
	_, leftMarked := left.(exactDecimal)
	_, rightMarked := right.(exactDecimal)
	if !leftMarked && !rightMarked {
		return nil, false, nil
	}
	var result decimalNumber
	switch operator {
	case "+":
		result, err = addDecimalNumbers(leftNumber, rightNumber)
	case "-":
		result, err = subtractDecimalNumbers(leftNumber, rightNumber)
	case "*":
		result, err = multiplyDecimalNumbers(leftNumber, rightNumber)
	case "/":
		result, err = divideDecimalNumbers(leftNumber, rightNumber, decimalDivisionScale)
	case "%":
		result, err = moduloDecimalNumbers(leftNumber, rightNumber)
	default:
		return nil, true, fmt.Errorf("unsupported decimal operator %q", operator)
	}
	if err != nil {
		return nil, true, err
	}
	value, err := decimalRuntimeResult(result)
	return value, true, err
}

type decimalSum struct {
	value decimalNumber
	has   bool
}

func exactDecimalAggregate(field *kitdbsql.Field, function string) bool {
	if field == nil || (function != "sum" && function != "avg") {
		return false
	}
	typeInfo, found := kitdbsql.LookupKind(field.Kind)
	return found && typeInfo.Family == kitdbsql.FamilyDecimal
}

func (sum *decimalSum) add(source string) error {
	number, err := parseDecimalNumber(source)
	if err != nil {
		return err
	}
	if !sum.has {
		sum.value, sum.has = number, true
		return nil
	}
	sum.value, err = addDecimalNumbers(sum.value, number)
	return err
}

func (sum *decimalSum) result(count int64, function string) (string, error) {
	if !sum.has {
		return "", fmt.Errorf("decimal aggregate has no value")
	}
	value := sum.value
	var err error
	if function == "avg" {
		value, err = divideDecimalNumbers(value, decimalFromInteger(count), decimalDivisionScale)
		if err != nil {
			return "", err
		}
	}
	return value.text()
}

func decimalScaleFromText(text string) int {
	canonical, err := canonicalDecimal(text)
	if err != nil {
		return 0
	}
	_, fraction := decimalParts(strings.TrimPrefix(canonical, "-"))
	return len(fraction)
}

func decimalTextFromValue(item any) (string, bool) {
	switch current := item.(type) {
	case string:
		return current, true
	case exactDecimal:
		return string(current), true
	case int64:
		return strconv.FormatInt(current, 10), true
	default:
		return "", false
	}
}
