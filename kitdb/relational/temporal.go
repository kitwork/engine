package relational

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

const (
	microsecondsPerSecond = int64(1_000_000)
	microsecondsPerMinute = 60 * microsecondsPerSecond
	microsecondsPerHour   = 60 * microsecondsPerMinute
	microsecondsPerDay    = 24 * microsecondsPerHour
)

// exactTemporal is an execution-only value. Durable KROW values remain
// canonical strings, while expressions retain enough structure for exact
// comparison and calendar-aware interval arithmetic.
type exactTemporal struct {
	kind      string
	text      string
	value     int64
	interval  temporalInterval
	precision *int
}

type temporalInterval struct {
	months       int32
	days         int32
	microseconds int64
}

func exactTemporalFieldKind(kind string) bool {
	switch kind {
	case "date", "time", "timestamp", "timestamptz", "interval":
		return true
	default:
		return false
	}
}

func exactTemporalTypeID(id kitdbsql.TypeID) bool {
	switch id {
	case kitdbsql.TypeDate, kitdbsql.TypeTime, kitdbsql.TypeTimestamp, kitdbsql.TypeTimestampTZ, kitdbsql.TypeInterval:
		return true
	default:
		return false
	}
}

func coerceTemporalField(field kitdbsql.Field, item any) (string, error) {
	if field.Kind == "datetime" {
		switch current := item.(type) {
		case string:
			return current, nil
		case time.Time:
			return current.UTC().Format(time.RFC3339Nano), nil
		default:
			return "", fmt.Errorf("field %q expects temporal text, got %T", field.Name, item)
		}
	}
	if !exactTemporalFieldKind(field.Kind) {
		return "", fmt.Errorf("field %q uses unsupported temporal kind %q", field.Name, field.Kind)
	}
	if current, ok := item.(exactTemporal); ok {
		converted, err := convertTemporal(current, field.Kind, field.TimePrecision)
		if err != nil {
			return "", fmt.Errorf("field %q: %w", field.Name, err)
		}
		return converted.text, nil
	}
	if current, ok := item.(time.Time); ok {
		converted, err := temporalFromTime(current, field.Kind, field.TimePrecision)
		if err != nil {
			return "", fmt.Errorf("field %q: %w", field.Name, err)
		}
		return converted.text, nil
	}
	if current, ok := item.(time.Duration); ok && field.Kind == "interval" {
		value := temporalInterval{microseconds: current.Microseconds()}
		return formatTemporalInterval(value), nil
	}
	text, ok := item.(string)
	if !ok {
		return "", fmt.Errorf("field %q expects %s text, got %T", field.Name, field.Kind, item)
	}
	parsed, err := parseTemporal(field.Kind, text, field.TimePrecision)
	if err != nil {
		return "", fmt.Errorf("field %q expects %s: %w", field.Name, field.Kind, err)
	}
	return parsed.text, nil
}

func parseTemporal(kind, source string, precision *int) (exactTemporal, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return exactTemporal{}, fmt.Errorf("value is empty")
	}
	switch kind {
	case "date":
		value, err := time.Parse("2006-01-02", source)
		if err != nil {
			return exactTemporal{}, fmt.Errorf("invalid DATE %q", source)
		}
		return temporalDateValue(value)
	case "time":
		microseconds, err := parseTimeOfDay(source, precision)
		if err != nil {
			return exactTemporal{}, err
		}
		return exactTemporal{
			kind: "time", value: microseconds, precision: cloneInt(precision),
			text: formatTimeOfDay(microseconds, precision),
		}, nil
	case "timestamp":
		value, err := parseTimestamp(source, false)
		if err != nil {
			return exactTemporal{}, err
		}
		return temporalTimestampValue(value, "timestamp", precision)
	case "timestamptz":
		value, err := parseTimestamp(source, true)
		if err != nil {
			return exactTemporal{}, err
		}
		return temporalTimestampValue(value.UTC(), "timestamptz", precision)
	case "interval":
		value, err := parseTemporalInterval(source)
		if err != nil {
			return exactTemporal{}, err
		}
		return exactTemporal{kind: "interval", text: formatTemporalInterval(value), interval: value}, nil
	default:
		return exactTemporal{}, fmt.Errorf("unsupported temporal kind %q", kind)
	}
}

func temporalFromTime(value time.Time, kind string, precision *int) (exactTemporal, error) {
	value = value.UTC()
	switch kind {
	case "date":
		return temporalDateValue(value)
	case "time":
		microseconds := int64(value.Hour())*microsecondsPerHour +
			int64(value.Minute())*microsecondsPerMinute + int64(value.Second())*microsecondsPerSecond +
			int64(value.Nanosecond())/1_000
		microseconds = roundTemporalMicroseconds(microseconds, value.Nanosecond()%1_000, precision)
		if microseconds >= microsecondsPerDay {
			microseconds = 0
		}
		return exactTemporal{
			kind: "time", value: microseconds, precision: cloneInt(precision),
			text: formatTimeOfDay(microseconds, precision),
		}, nil
	case "timestamp", "timestamptz":
		return temporalTimestampValue(value, kind, precision)
	default:
		return exactTemporal{}, fmt.Errorf("cannot convert time.Time to %s", strings.ToUpper(kind))
	}
}

func convertTemporal(value exactTemporal, kind string, precision *int) (exactTemporal, error) {
	if value.kind == kind {
		if kind == "time" {
			value.value = roundTemporalMicroseconds(value.value, 0, precision)
			value.text = formatTimeOfDay(value.value, precision)
			value.precision = cloneInt(precision)
		}
		if kind == "timestamp" || kind == "timestamptz" {
			instant := time.UnixMicro(value.value).UTC()
			return temporalTimestampValue(instant, kind, precision)
		}
		return value, nil
	}
	switch {
	case value.kind == "date" && (kind == "timestamp" || kind == "timestamptz"):
		return temporalTimestampValue(dateFromDays(value.value), kind, precision)
	case value.kind == "timestamp" && kind == "timestamptz":
		return temporalTimestampValue(time.UnixMicro(value.value).UTC(), kind, precision)
	case value.kind == "timestamptz" && kind == "timestamp":
		return temporalTimestampValue(time.UnixMicro(value.value).UTC(), kind, precision)
	case (value.kind == "timestamp" || value.kind == "timestamptz") && kind == "date":
		return temporalDateValue(time.UnixMicro(value.value).UTC())
	case (value.kind == "timestamp" || value.kind == "timestamptz") && kind == "time":
		return temporalFromTime(time.UnixMicro(value.value).UTC(), "time", precision)
	default:
		return exactTemporal{}, fmt.Errorf("cannot cast %s to %s", strings.ToUpper(value.kind), strings.ToUpper(kind))
	}
}

func temporalDateValue(value time.Time) (exactTemporal, error) {
	if value.Year() < 1 || value.Year() > 9999 {
		return exactTemporal{}, fmt.Errorf("DATE is outside years 0001..9999")
	}
	value = time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
	return exactTemporal{kind: "date", value: value.Unix() / 86_400, text: value.Format("2006-01-02")}, nil
}

func temporalTimestampValue(value time.Time, kind string, precision *int) (exactTemporal, error) {
	value = roundTemporalTime(value.UTC(), precision)
	if value.Year() < 1 || value.Year() > 9999 {
		return exactTemporal{}, fmt.Errorf("%s is outside years 0001..9999", strings.ToUpper(kind))
	}
	formatPrecision := precision
	if kind == "timestamptz" && precision == nil {
		// A fixed-width canonical value keeps byte-ordered indexes chronological:
		// without the fraction, trailing Z sorts after '.'.
		maximum := kitdbsql.MaximumTemporalPrecision
		formatPrecision = &maximum
	}
	text := formatTimestamp(value, formatPrecision)
	if kind == "timestamptz" {
		text += "Z"
	}
	return exactTemporal{
		kind: kind, text: text, value: value.UnixMicro(), precision: cloneInt(precision),
	}, nil
}

func parseTimestamp(source string, withTimezone bool) (time.Time, error) {
	normalized := strings.Replace(source, " ", "T", 1)
	if len(normalized) == len("2006-01-02") {
		value, err := time.Parse("2006-01-02", normalized)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid timestamp %q", source)
		}
		return value, nil
	}
	if withTimezone {
		normalized = normalizeTimestampOffset(normalized)
		if value, err := time.Parse(time.RFC3339Nano, normalized); err == nil {
			return value.UTC(), nil
		}
	}
	value, err := time.Parse("2006-01-02T15:04:05.999999999", normalized)
	if err != nil {
		profile := "TIMESTAMP"
		if withTimezone {
			profile = "TIMESTAMPTZ"
		}
		return time.Time{}, fmt.Errorf("invalid %s %q", profile, source)
	}
	return value, nil
}

func normalizeTimestampOffset(source string) string {
	timePosition := strings.IndexByte(source, 'T')
	if strings.HasSuffix(source, "z") {
		return strings.TrimSuffix(source, "z") + "Z"
	}
	if timePosition < 0 || strings.HasSuffix(source, "Z") {
		return source
	}
	if len(source) >= 3 {
		position := len(source) - 3
		if position > timePosition && (source[position] == '+' || source[position] == '-') &&
			source[position+1] >= '0' && source[position+1] <= '9' && source[position+2] >= '0' && source[position+2] <= '9' {
			return source + ":00"
		}
	}
	if len(source) >= 5 {
		position := len(source) - 5
		if position > timePosition && (source[position] == '+' || source[position] == '-') &&
			source[position+1] >= '0' && source[position+1] <= '9' && source[position+2] >= '0' && source[position+2] <= '9' &&
			source[position+3] >= '0' && source[position+3] <= '9' && source[position+4] >= '0' && source[position+4] <= '9' {
			return source[:position+3] + ":" + source[position+3:]
		}
	}
	return source
}

func parseTimeOfDay(source string, precision *int) (int64, error) {
	if source == "24:00" || source == "24:00:00" || strings.HasPrefix(source, "24:00:00.") {
		if strings.TrimRight(strings.TrimPrefix(source, "24:00:00"), "0.") == "" {
			return microsecondsPerDay, nil
		}
		return 0, fmt.Errorf("invalid TIME %q", source)
	}
	value, err := time.Parse("15:04:05.999999999", source)
	if err != nil {
		value, err = time.Parse("15:04", source)
	}
	if err != nil {
		return 0, fmt.Errorf("invalid TIME %q", source)
	}
	microseconds := int64(value.Hour())*microsecondsPerHour +
		int64(value.Minute())*microsecondsPerMinute + int64(value.Second())*microsecondsPerSecond +
		int64(value.Nanosecond())/1_000
	microseconds = roundTemporalMicroseconds(microseconds, value.Nanosecond()%1_000, precision)
	if microseconds > microsecondsPerDay {
		return 0, fmt.Errorf("TIME %q is outside 00:00:00..24:00:00", source)
	}
	return microseconds, nil
}

func roundTemporalTime(value time.Time, precision *int) time.Time {
	p := temporalPrecision(precision)
	unit := int64(1)
	for index := p; index < 9; index++ {
		unit *= 10
	}
	nanoseconds := int64(value.Nanosecond())
	rounded := ((nanoseconds + unit/2) / unit) * unit
	value = value.Truncate(time.Second)
	if rounded == int64(time.Second) {
		return value.Add(time.Second)
	}
	return value.Add(time.Duration(rounded))
}

func roundTemporalMicroseconds(value int64, remainingNanoseconds int, precision *int) int64 {
	p := temporalPrecision(precision)
	unit := int64(1)
	for index := p; index < 6; index++ {
		unit *= 10
	}
	if unit == 1 {
		if remainingNanoseconds >= 500 {
			return value + 1
		}
		return value
	}
	return ((value + unit/2) / unit) * unit
}

func temporalPrecision(precision *int) int {
	if precision == nil {
		return kitdbsql.MaximumTemporalPrecision
	}
	return *precision
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func formatTimestamp(value time.Time, precision *int) string {
	base := value.UTC().Format("2006-01-02T15:04:05")
	return base + formatTemporalFraction(value.Nanosecond()/1_000, precision)
}

func formatTimeOfDay(value int64, precision *int) string {
	if value == microsecondsPerDay {
		return "24:00:00" + formatTemporalFraction(0, precision)
	}
	hours := value / microsecondsPerHour
	value %= microsecondsPerHour
	minutes := value / microsecondsPerMinute
	value %= microsecondsPerMinute
	seconds := value / microsecondsPerSecond
	microseconds := value % microsecondsPerSecond
	return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, seconds) + formatTemporalFraction(int(microseconds), precision)
}

func formatTemporalFraction(microseconds int, precision *int) string {
	p := temporalPrecision(precision)
	if p == 0 {
		return ""
	}
	text := fmt.Sprintf("%06d", microseconds)[:p]
	if precision == nil {
		text = strings.TrimRight(text, "0")
	}
	if text == "" {
		return ""
	}
	return "." + text
}

func dateFromDays(days int64) time.Time {
	return time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, int(days))
}

func parseTemporalInterval(source string) (temporalInterval, error) {
	parts := strings.Fields(strings.ToLower(strings.TrimSpace(source)))
	if len(parts) == 0 {
		return temporalInterval{}, fmt.Errorf("invalid INTERVAL %q", source)
	}
	var result temporalInterval
	for position := 0; position < len(parts); {
		if strings.Contains(parts[position], ":") {
			microseconds, err := parseIntervalClock(parts[position])
			if err != nil {
				return temporalInterval{}, fmt.Errorf("invalid INTERVAL %q: %w", source, err)
			}
			if result.microseconds, err = checkedAddInt64(result.microseconds, microseconds); err != nil {
				return temporalInterval{}, fmt.Errorf("INTERVAL %q is out of range", source)
			}
			position++
			continue
		}
		if position+1 >= len(parts) {
			return temporalInterval{}, fmt.Errorf("invalid INTERVAL %q: missing unit", source)
		}
		number, unit := parts[position], strings.TrimSuffix(parts[position+1], "s")
		position += 2
		var err error
		switch unit {
		case "year":
			result.months, err = addIntervalInt32(result.months, number, 12)
		case "mon", "month":
			result.months, err = addIntervalInt32(result.months, number, 1)
		case "week":
			result.days, err = addIntervalInt32(result.days, number, 7)
		case "day":
			result.days, err = addIntervalInt32(result.days, number, 1)
		case "hour":
			result.microseconds, err = addIntervalMicros(result.microseconds, number, microsecondsPerHour)
		case "min", "minute":
			result.microseconds, err = addIntervalMicros(result.microseconds, number, microsecondsPerMinute)
		case "sec", "second":
			result.microseconds, err = addIntervalMicros(result.microseconds, number, microsecondsPerSecond)
		case "millisecond":
			result.microseconds, err = addIntervalMicros(result.microseconds, number, 1_000)
		case "microsecond":
			result.microseconds, err = addIntervalMicros(result.microseconds, number, 1)
		default:
			return temporalInterval{}, fmt.Errorf("invalid INTERVAL %q: unsupported unit %q", source, parts[position-1])
		}
		if err != nil {
			return temporalInterval{}, fmt.Errorf("INTERVAL %q is out of range", source)
		}
	}
	return result, nil
}

func addIntervalInt32(current int32, source string, multiplier int64) (int32, error) {
	const (
		minimumInt32 = int64(-1 << 31)
		maximumInt32 = int64(1<<31 - 1)
	)
	value, err := strconv.ParseInt(source, 10, 64)
	if err != nil || multiplier <= 0 || value > maximumInt32/multiplier || value < minimumInt32/multiplier {
		return 0, fmt.Errorf("out of range")
	}
	value *= multiplier
	result := int64(current) + value
	if result < -1<<31 || result > 1<<31-1 {
		return 0, fmt.Errorf("out of range")
	}
	return int32(result), nil
}

func addIntervalMicros(current int64, source string, multiplier int64) (int64, error) {
	value, err := scaledDecimalInt64(source, multiplier)
	if err != nil {
		return 0, err
	}
	return checkedAddInt64(current, value)
}

func scaledDecimalInt64(source string, multiplier int64) (int64, error) {
	number, err := parseDecimalNumber(source)
	if err != nil {
		return 0, err
	}
	var numerator big.Int
	numerator.Mul(&number.coefficient, big.NewInt(multiplier))
	denominator := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(number.scale)), nil)
	var quotient, remainder big.Int
	quotient.QuoRem(&numerator, denominator, &remainder)
	var twice big.Int
	twice.Abs(&remainder)
	twice.Lsh(&twice, 1)
	if twice.Cmp(denominator) >= 0 {
		if numerator.Sign() < 0 {
			quotient.Sub(&quotient, big.NewInt(1))
		} else {
			quotient.Add(&quotient, big.NewInt(1))
		}
	}
	if !quotient.IsInt64() {
		return 0, fmt.Errorf("out of range")
	}
	return quotient.Int64(), nil
}

func parseIntervalClock(source string) (int64, error) {
	negative := strings.HasPrefix(source, "-")
	if negative || strings.HasPrefix(source, "+") {
		source = source[1:]
	}
	parts := strings.Split(source, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("clock must be HH:MM:SS")
	}
	hours, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid hours")
	}
	minutes, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || minutes < 0 || minutes > 59 {
		return 0, fmt.Errorf("invalid minutes")
	}
	seconds, err := scaledDecimalInt64(parts[2], microsecondsPerSecond)
	if err != nil || seconds < 0 || seconds >= microsecondsPerMinute {
		return 0, fmt.Errorf("invalid seconds")
	}
	limit := uint64(1<<63 - 1)
	if negative {
		limit++
	}
	remainder := uint64(minutes*microsecondsPerMinute + seconds)
	if hours > (limit-remainder)/uint64(microsecondsPerHour) {
		return 0, fmt.Errorf("clock is out of range")
	}
	magnitude := hours*uint64(microsecondsPerHour) + remainder
	if negative {
		if magnitude == uint64(1)<<63 {
			return int64(-1 << 63), nil
		}
		return -int64(magnitude), nil
	}
	return int64(magnitude), nil
}

func checkedAddInt64(left, right int64) (int64, error) {
	const (
		minimumInt64 = int64(-1 << 63)
		maximumInt64 = int64(1<<63 - 1)
	)
	if (right > 0 && left > maximumInt64-right) || (right < 0 && left < minimumInt64-right) {
		return 0, fmt.Errorf("out of range")
	}
	return left + right, nil
}

func checkedSubtractInt64(left, right int64) (int64, error) {
	const (
		minimumInt64 = int64(-1 << 63)
		maximumInt64 = int64(1<<63 - 1)
	)
	if (right > 0 && left < minimumInt64+right) || (right < 0 && left > maximumInt64+right) {
		return 0, fmt.Errorf("out of range")
	}
	return left - right, nil
}

func formatTemporalInterval(value temporalInterval) string {
	parts := make([]string, 0, 3)
	if value.months != 0 {
		parts = append(parts, fmt.Sprintf("%d mons", value.months))
	}
	if value.days != 0 {
		parts = append(parts, fmt.Sprintf("%d days", value.days))
	}
	if value.microseconds != 0 || len(parts) == 0 {
		parts = append(parts, formatIntervalClock(value.microseconds))
	}
	return strings.Join(parts, " ")
}

func formatIntervalClock(value int64) string {
	negative := value < 0
	absolute := uint64(value)
	if negative {
		absolute = uint64(-(value + 1)) + 1
	}
	hours := absolute / uint64(microsecondsPerHour)
	absolute %= uint64(microsecondsPerHour)
	minutes := absolute / uint64(microsecondsPerMinute)
	absolute %= uint64(microsecondsPerMinute)
	seconds := absolute / uint64(microsecondsPerSecond)
	microseconds := absolute % uint64(microsecondsPerSecond)
	prefix := ""
	if negative {
		prefix = "-"
	}
	result := fmt.Sprintf("%s%02d:%02d:%02d", prefix, hours, minutes, seconds)
	if microseconds != 0 {
		result += "." + strings.TrimRight(fmt.Sprintf("%06d", microseconds), "0")
	}
	return result
}

func compareTemporalField(field kitdbsql.Field, left, right any) (int, bool) {
	leftValue, leftOK := temporalComparable(field, left)
	rightValue, rightOK := temporalComparable(field, right)
	if !leftOK || !rightOK {
		return 0, false
	}
	return compareExactTemporals(leftValue, rightValue), true
}

func temporalComparable(field kitdbsql.Field, item any) (exactTemporal, bool) {
	if current, ok := item.(exactTemporal); ok {
		converted, err := convertTemporal(current, field.Kind, field.TimePrecision)
		return converted, err == nil
	}
	text, ok := item.(string)
	if !ok {
		return exactTemporal{}, false
	}
	parsed, err := parseTemporal(field.Kind, text, field.TimePrecision)
	return parsed, err == nil
}

func compareExactTemporals(left, right exactTemporal) int {
	if left.kind == "interval" && right.kind == "interval" {
		return intervalComparable(left.interval).Cmp(intervalComparable(right.interval))
	}
	if left.value < right.value {
		return -1
	}
	if left.value > right.value {
		return 1
	}
	return 0
}

func intervalComparable(value temporalInterval) *big.Int {
	result := new(big.Int).SetInt64(int64(value.months))
	result.Mul(result, big.NewInt(30*microsecondsPerDay))
	days := new(big.Int).Mul(big.NewInt(int64(value.days)), big.NewInt(microsecondsPerDay))
	result.Add(result, days)
	return result.Add(result, big.NewInt(value.microseconds))
}

func evaluateTemporalBinary(operator string, left, right any) (any, bool, error) {
	leftTemporal, leftIsTemporal := left.(exactTemporal)
	rightTemporal, rightIsTemporal := right.(exactTemporal)
	if !leftIsTemporal && !rightIsTemporal {
		return nil, false, nil
	}
	if operator != "+" && operator != "-" {
		return nil, true, fmt.Errorf("temporal values support only + and - arithmetic")
	}
	if leftIsTemporal && leftTemporal.kind == "date" && !rightIsTemporal {
		days, err := integerValue(right)
		if err != nil {
			return nil, true, fmt.Errorf("DATE arithmetic requires an integer day count")
		}
		if operator == "-" {
			days = -days
		}
		value, err := addDateDays(leftTemporal, days)
		return value, true, err
	}
	if !leftIsTemporal && rightIsTemporal && rightTemporal.kind == "date" && operator == "+" {
		days, err := integerValue(left)
		if err != nil {
			return nil, true, fmt.Errorf("DATE arithmetic requires an integer day count")
		}
		value, err := addDateDays(rightTemporal, days)
		return value, true, err
	}
	if !leftIsTemporal || !rightIsTemporal {
		return nil, true, fmt.Errorf("incompatible temporal arithmetic operands")
	}
	if leftTemporal.kind == "date" && rightTemporal.kind == "date" && operator == "-" {
		return leftTemporal.value - rightTemporal.value, true, nil
	}
	if leftTemporal.kind == "interval" && rightTemporal.kind == "interval" {
		value, err := addIntervals(leftTemporal.interval, rightTemporal.interval, operator == "-")
		if err != nil {
			return nil, true, err
		}
		return exactTemporal{kind: "interval", interval: value, text: formatTemporalInterval(value)}, true, nil
	}
	if rightTemporal.kind == "interval" {
		if operator == "-" {
			var err error
			rightTemporal.interval, err = negateInterval(rightTemporal.interval)
			if err != nil {
				return nil, true, err
			}
		}
		value, err := addIntervalToTemporal(leftTemporal, rightTemporal.interval)
		return value, true, err
	}
	if leftTemporal.kind == "interval" && operator == "+" {
		value, err := addIntervalToTemporal(rightTemporal, leftTemporal.interval)
		return value, true, err
	}
	if operator == "-" && (leftTemporal.kind == "timestamp" || leftTemporal.kind == "timestamptz") &&
		leftTemporal.kind == rightTemporal.kind {
		microseconds, err := checkedSubtractInt64(leftTemporal.value, rightTemporal.value)
		if err != nil {
			return nil, true, fmt.Errorf("timestamp difference is out of range")
		}
		interval := temporalInterval{microseconds: microseconds}
		return exactTemporal{kind: "interval", interval: interval, text: formatTemporalInterval(interval)}, true, nil
	}
	return nil, true, fmt.Errorf("incompatible %s and %s arithmetic", strings.ToUpper(leftTemporal.kind), strings.ToUpper(rightTemporal.kind))
}

func addDateDays(value exactTemporal, days int64) (exactTemporal, error) {
	result, err := checkedAddInt64(value.value, days)
	if err != nil || result < -719162 || result > 2932896 {
		return exactTemporal{}, fmt.Errorf("DATE result is outside years 0001..9999")
	}
	date := dateFromDays(result)
	return temporalDateValue(date)
}

func addIntervals(left, right temporalInterval, subtract bool) (temporalInterval, error) {
	if subtract {
		var err error
		right, err = negateInterval(right)
		if err != nil {
			return temporalInterval{}, err
		}
	}
	months := int64(left.months) + int64(right.months)
	days := int64(left.days) + int64(right.days)
	microseconds, err := checkedAddInt64(left.microseconds, right.microseconds)
	if months < -1<<31 || months > 1<<31-1 || days < -1<<31 || days > 1<<31-1 || err != nil {
		return temporalInterval{}, fmt.Errorf("INTERVAL result is out of range")
	}
	return temporalInterval{months: int32(months), days: int32(days), microseconds: microseconds}, nil
}

func negateInterval(value temporalInterval) (temporalInterval, error) {
	if value.months == -1<<31 || value.days == -1<<31 || value.microseconds == -1<<63 {
		return temporalInterval{}, fmt.Errorf("INTERVAL result is out of range")
	}
	return temporalInterval{months: -value.months, days: -value.days, microseconds: -value.microseconds}, nil
}

func addIntervalToTemporal(value exactTemporal, interval temporalInterval) (exactTemporal, error) {
	switch value.kind {
	case "date", "timestamp", "timestamptz":
		base := dateFromDays(value.value)
		resultKind := value.kind
		precision := value.precision
		if value.kind != "date" {
			base = time.UnixMicro(value.value).UTC()
		} else {
			resultKind = "timestamp"
		}
		wholeDays := interval.microseconds / microsecondsPerDay
		remainder := interval.microseconds % microsecondsPerDay
		days := int64(interval.days) + wholeDays
		if days < -4_000_000 || days > 4_000_000 {
			return exactTemporal{}, fmt.Errorf("temporal result is outside years 0001..9999")
		}
		base = base.AddDate(0, int(interval.months), int(days))
		base = base.Add(time.Duration(remainder) * time.Microsecond)
		if base.Year() < 1 || base.Year() > 9999 {
			return exactTemporal{}, fmt.Errorf("temporal result is outside years 0001..9999")
		}
		return temporalTimestampValue(base, resultKind, precision)
	case "time":
		if interval.months != 0 || interval.days != 0 {
			return exactTemporal{}, fmt.Errorf("TIME arithmetic does not accept month/day interval components")
		}
		// Reduce first so an otherwise valid clock wrap cannot overflow int64.
		result := (value.value + interval.microseconds%microsecondsPerDay) % microsecondsPerDay
		if result < 0 {
			result += microsecondsPerDay
		}
		return exactTemporal{
			kind: "time", value: result, precision: cloneInt(value.precision),
			text: formatTimeOfDay(result, value.precision),
		}, nil
	default:
		return exactTemporal{}, fmt.Errorf("cannot add INTERVAL to %s", strings.ToUpper(value.kind))
	}
}

func truncateTemporal(unit string, value exactTemporal) (exactTemporal, error) {
	unit = strings.ToLower(strings.TrimSpace(unit))
	switch value.kind {
	case "date", "timestamp", "timestamptz":
		instant := dateFromDays(value.value)
		if value.kind != "date" {
			instant = time.UnixMicro(value.value).UTC()
		}
		year, month, day := instant.Date()
		hour, minute, second := instant.Clock()
		nanosecond := instant.Nanosecond()
		switch unit {
		case "millennium":
			year = ((year-1)/1000)*1000 + 1
			month, day, hour, minute, second, nanosecond = time.January, 1, 0, 0, 0, 0
		case "century":
			year = ((year-1)/100)*100 + 1
			month, day, hour, minute, second, nanosecond = time.January, 1, 0, 0, 0, 0
		case "decade":
			year = (year / 10) * 10
			if year == 0 {
				year = 1
			}
			month, day, hour, minute, second, nanosecond = time.January, 1, 0, 0, 0, 0
		case "year":
			month, day, hour, minute, second, nanosecond = time.January, 1, 0, 0, 0, 0
		case "quarter":
			month = time.Month(((int(month)-1)/3)*3 + 1)
			day, hour, minute, second, nanosecond = 1, 0, 0, 0, 0
		case "month":
			day, hour, minute, second, nanosecond = 1, 0, 0, 0, 0
		case "week":
			instant = time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
			offset := (int(instant.Weekday()) + 6) % 7
			instant = instant.AddDate(0, 0, -offset)
			year, month, day = instant.Date()
			hour, minute, second, nanosecond = 0, 0, 0, 0
		case "day":
			hour, minute, second, nanosecond = 0, 0, 0, 0
		case "hour":
			if value.kind == "date" {
				return exactTemporal{}, fmt.Errorf("kitdb SQL: DATE_TRUNC hour does not accept DATE")
			}
			minute, second, nanosecond = 0, 0, 0
		case "minute":
			if value.kind == "date" {
				return exactTemporal{}, fmt.Errorf("kitdb SQL: DATE_TRUNC minute does not accept DATE")
			}
			second, nanosecond = 0, 0
		case "second":
			if value.kind == "date" {
				return exactTemporal{}, fmt.Errorf("kitdb SQL: DATE_TRUNC second does not accept DATE")
			}
			nanosecond = 0
		case "millisecond":
			if value.kind == "date" {
				return exactTemporal{}, fmt.Errorf("kitdb SQL: DATE_TRUNC millisecond does not accept DATE")
			}
			nanosecond = nanosecond / 1_000_000 * 1_000_000
		case "microsecond":
			if value.kind == "date" {
				return exactTemporal{}, fmt.Errorf("kitdb SQL: DATE_TRUNC microsecond does not accept DATE")
			}
			nanosecond = nanosecond / 1_000 * 1_000
		default:
			return exactTemporal{}, fmt.Errorf("kitdb SQL: unsupported DATE_TRUNC unit %q", unit)
		}
		result := time.Date(year, month, day, hour, minute, second, nanosecond, time.UTC)
		if value.kind == "date" {
			return temporalDateValue(result)
		}
		return temporalTimestampValue(result, value.kind, value.precision)
	case "time":
		divisor := int64(0)
		switch unit {
		case "hour":
			divisor = microsecondsPerHour
		case "minute":
			divisor = microsecondsPerMinute
		case "second":
			divisor = microsecondsPerSecond
		case "millisecond":
			divisor = 1_000
		case "microsecond":
			divisor = 1
		default:
			return exactTemporal{}, fmt.Errorf("kitdb SQL: unsupported DATE_TRUNC unit %q for TIME", unit)
		}
		result := value.value / divisor * divisor
		return exactTemporal{
			kind: "time", value: result, precision: cloneInt(value.precision),
			text: formatTimeOfDay(result, value.precision),
		}, nil
	case "interval":
		result := value.interval
		switch unit {
		case "year":
			result.months = result.months / 12 * 12
			result.days, result.microseconds = 0, 0
		case "month":
			result.days, result.microseconds = 0, 0
		case "day":
			result.microseconds = 0
		case "hour":
			result.microseconds = result.microseconds / microsecondsPerHour * microsecondsPerHour
		case "minute":
			result.microseconds = result.microseconds / microsecondsPerMinute * microsecondsPerMinute
		case "second":
			result.microseconds = result.microseconds / microsecondsPerSecond * microsecondsPerSecond
		case "millisecond":
			result.microseconds = result.microseconds / 1_000 * 1_000
		case "microsecond":
		default:
			return exactTemporal{}, fmt.Errorf("kitdb SQL: unsupported DATE_TRUNC unit %q for INTERVAL", unit)
		}
		return exactTemporal{kind: "interval", interval: result, text: formatTemporalInterval(result)}, nil
	default:
		return exactTemporal{}, fmt.Errorf("kitdb SQL: DATE_TRUNC does not accept %s", strings.ToUpper(value.kind))
	}
}

func temporalPart(unit string, value exactTemporal) (float64, error) {
	unit = strings.ToLower(strings.TrimSpace(unit))
	if value.kind == "interval" {
		months, days, microseconds := value.interval.months, value.interval.days, value.interval.microseconds
		absolute := uint64(microseconds)
		if microseconds < 0 {
			absolute = uint64(-(microseconds + 1)) + 1
		}
		sign := float64(1)
		if microseconds < 0 {
			sign = -1
		}
		switch unit {
		case "year":
			return float64(months / 12), nil
		case "month":
			return float64(months % 12), nil
		case "day":
			return float64(days), nil
		case "hour":
			return sign * float64(absolute/uint64(microsecondsPerHour)), nil
		case "minute":
			return sign * float64((absolute%uint64(microsecondsPerHour))/uint64(microsecondsPerMinute)), nil
		case "second":
			return sign * float64(absolute%uint64(microsecondsPerMinute)) / float64(microsecondsPerSecond), nil
		case "epoch":
			seconds := new(big.Rat).SetInt(intervalComparable(value.interval))
			seconds.Quo(seconds, big.NewRat(microsecondsPerSecond, 1))
			result, _ := seconds.Float64()
			return result, nil
		default:
			return 0, fmt.Errorf("kitdb SQL: unsupported DATE_PART unit %q for INTERVAL", unit)
		}
	}
	if value.kind == "time" {
		microseconds := value.value
		switch unit {
		case "hour":
			return float64(microseconds / microsecondsPerHour), nil
		case "minute":
			return float64((microseconds % microsecondsPerHour) / microsecondsPerMinute), nil
		case "second":
			return float64(microseconds%microsecondsPerMinute) / float64(microsecondsPerSecond), nil
		case "epoch":
			return float64(microseconds) / float64(microsecondsPerSecond), nil
		default:
			return 0, fmt.Errorf("kitdb SQL: unsupported DATE_PART unit %q for TIME", unit)
		}
	}
	instant := dateFromDays(value.value)
	if value.kind == "timestamp" || value.kind == "timestamptz" {
		instant = time.UnixMicro(value.value).UTC()
	}
	switch unit {
	case "millennium":
		return float64((instant.Year()-1)/1000 + 1), nil
	case "century":
		return float64((instant.Year()-1)/100 + 1), nil
	case "decade":
		return float64(instant.Year() / 10), nil
	case "year":
		return float64(instant.Year()), nil
	case "quarter":
		return float64((int(instant.Month())-1)/3 + 1), nil
	case "month":
		return float64(instant.Month()), nil
	case "week":
		_, week := instant.ISOWeek()
		return float64(week), nil
	case "day":
		return float64(instant.Day()), nil
	case "doy":
		return float64(instant.YearDay()), nil
	case "dow":
		return float64(instant.Weekday()), nil
	case "isodow":
		weekday := int(instant.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		return float64(weekday), nil
	case "hour":
		return float64(instant.Hour()), nil
	case "minute":
		return float64(instant.Minute()), nil
	case "second":
		return float64(instant.Second()) + float64(instant.Nanosecond())/float64(time.Second), nil
	case "epoch":
		return float64(instant.UnixMicro()) / float64(microsecondsPerSecond), nil
	case "timezone", "timezone_hour", "timezone_minute":
		if value.kind != "timestamptz" {
			return 0, fmt.Errorf("kitdb SQL: DATE_PART %s requires TIMESTAMPTZ", unit)
		}
		return 0, nil
	default:
		return 0, fmt.Errorf("kitdb SQL: unsupported DATE_PART unit %q", unit)
	}
}
