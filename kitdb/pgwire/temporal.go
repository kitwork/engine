package pgwire

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	postgresMicrosecondsPerSecond = int64(1_000_000)
	postgresMicrosecondsPerMinute = 60 * postgresMicrosecondsPerSecond
	postgresMicrosecondsPerHour   = 60 * postgresMicrosecondsPerMinute
	postgresMicrosecondsPerDay    = 24 * postgresMicrosecondsPerHour
)

var postgresTemporalEpoch = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// Interval is PostgreSQL's exact binary interval shape. Months and days stay
// separate because a calendar month is not a fixed number of microseconds.
type Interval struct {
	Microseconds int64
	Days         int32
	Months       int32
}

func EncodeDateBinary(source string) ([]byte, error) {
	value, err := time.Parse("2006-01-02", strings.TrimSpace(source))
	if err != nil {
		return nil, fmt.Errorf("invalid date")
	}
	days := value.Unix()/86_400 - postgresTemporalEpoch.Unix()/86_400
	if days < -1<<31 || days > 1<<31-1 {
		return nil, fmt.Errorf("date is outside PostgreSQL binary range")
	}
	result := make([]byte, 4)
	binary.BigEndian.PutUint32(result, uint32(int32(days)))
	return result, nil
}

func DecodeDateBinary(data []byte) (string, error) {
	if len(data) != 4 {
		return "", fmt.Errorf("invalid binary date length")
	}
	days := int64(int32(binary.BigEndian.Uint32(data)))
	value := postgresTemporalEpoch.AddDate(0, 0, int(days))
	if value.Year() < 1 || value.Year() > 9999 {
		return "", fmt.Errorf("binary date is outside KitDB's bounded range")
	}
	return value.Format("2006-01-02"), nil
}

func EncodeTimeBinary(source string) ([]byte, error) {
	microseconds, err := parseWireTime(source)
	if err != nil {
		return nil, err
	}
	result := make([]byte, 8)
	binary.BigEndian.PutUint64(result, uint64(microseconds))
	return result, nil
}

func DecodeTimeBinary(data []byte) (string, error) {
	if len(data) != 8 {
		return "", fmt.Errorf("invalid binary time length")
	}
	microseconds := int64(binary.BigEndian.Uint64(data))
	if microseconds < 0 || microseconds > postgresMicrosecondsPerDay {
		return "", fmt.Errorf("binary time is outside 00:00:00..24:00:00")
	}
	return formatWireTime(microseconds), nil
}

func EncodeTimestampBinary(source string, withTimezone bool) ([]byte, error) {
	value, err := parseWireTimestamp(source, withTimezone)
	if err != nil {
		return nil, err
	}
	microseconds := value.UnixMicro() - postgresTemporalEpoch.UnixMicro()
	result := make([]byte, 8)
	binary.BigEndian.PutUint64(result, uint64(microseconds))
	return result, nil
}

func DecodeTimestampBinary(data []byte, withTimezone bool) (string, error) {
	if len(data) != 8 {
		return "", fmt.Errorf("invalid binary timestamp length")
	}
	microseconds := int64(binary.BigEndian.Uint64(data))
	unixMicroseconds := postgresTemporalEpoch.UnixMicro() + microseconds
	if microseconds > 0 && unixMicroseconds < postgresTemporalEpoch.UnixMicro() ||
		microseconds < 0 && unixMicroseconds > postgresTemporalEpoch.UnixMicro() {
		return "", fmt.Errorf("binary timestamp is outside KitDB's bounded range")
	}
	value := time.UnixMicro(unixMicroseconds).UTC()
	if value.Year() < 1 || value.Year() > 9999 {
		return "", fmt.Errorf("binary timestamp is outside KitDB's bounded range")
	}
	text := formatWireTimestamp(value)
	if withTimezone {
		text += "Z"
	}
	return text, nil
}

func EncodeIntervalBinary(source string) ([]byte, error) {
	value, err := parseWireInterval(source)
	if err != nil {
		return nil, err
	}
	result := make([]byte, 16)
	binary.BigEndian.PutUint64(result[0:8], uint64(value.Microseconds))
	binary.BigEndian.PutUint32(result[8:12], uint32(value.Days))
	binary.BigEndian.PutUint32(result[12:16], uint32(value.Months))
	return result, nil
}

func DecodeIntervalBinary(data []byte) (string, error) {
	if len(data) != 16 {
		return "", fmt.Errorf("invalid binary interval length")
	}
	value := Interval{
		Microseconds: int64(binary.BigEndian.Uint64(data[0:8])),
		Days:         int32(binary.BigEndian.Uint32(data[8:12])),
		Months:       int32(binary.BigEndian.Uint32(data[12:16])),
	}
	return formatWireInterval(value), nil
}

func parseWireTime(source string) (int64, error) {
	source = strings.TrimSpace(source)
	if source == "24:00:00" || source == "24:00" {
		return postgresMicrosecondsPerDay, nil
	}
	value, err := time.Parse("15:04:05.999999", source)
	if err != nil {
		return 0, fmt.Errorf("invalid time")
	}
	return int64(value.Hour())*postgresMicrosecondsPerHour +
		int64(value.Minute())*postgresMicrosecondsPerMinute +
		int64(value.Second())*postgresMicrosecondsPerSecond + int64(value.Nanosecond())/1_000, nil
}

func parseWireTimestamp(source string, withTimezone bool) (time.Time, error) {
	source = strings.TrimSpace(source)
	normalized := strings.Replace(source, " ", "T", 1)
	if withTimezone {
		if strings.HasSuffix(normalized, "+00") {
			normalized = strings.TrimSuffix(normalized, "+00") + "Z"
		}
		if value, err := time.Parse(time.RFC3339Nano, normalized); err == nil {
			return value.UTC(), nil
		}
	}
	value, err := time.Parse("2006-01-02T15:04:05.999999", normalized)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid timestamp")
	}
	return value.UTC(), nil
}

func formatWireTimestamp(value time.Time) string {
	base := value.UTC().Format("2006-01-02T15:04:05")
	fraction := strings.TrimRight(fmt.Sprintf("%06d", value.Nanosecond()/1_000), "0")
	if fraction != "" {
		base += "." + fraction
	}
	return base
}

func formatWireTime(value int64) string {
	if value == postgresMicrosecondsPerDay {
		return "24:00:00"
	}
	hours := value / postgresMicrosecondsPerHour
	value %= postgresMicrosecondsPerHour
	minutes := value / postgresMicrosecondsPerMinute
	value %= postgresMicrosecondsPerMinute
	seconds := value / postgresMicrosecondsPerSecond
	microseconds := value % postgresMicrosecondsPerSecond
	result := fmt.Sprintf("%02d:%02d:%02d", hours, minutes, seconds)
	if microseconds != 0 {
		result += "." + strings.TrimRight(fmt.Sprintf("%06d", microseconds), "0")
	}
	return result
}

func parseWireInterval(source string) (Interval, error) {
	parts := strings.Fields(strings.ToLower(strings.TrimSpace(source)))
	var value Interval
	for position := 0; position < len(parts); {
		if strings.Contains(parts[position], ":") {
			microseconds, err := parseWireIntervalClock(parts[position])
			if err != nil {
				return Interval{}, err
			}
			value.Microseconds = microseconds
			position++
			continue
		}
		if position+1 >= len(parts) {
			return Interval{}, fmt.Errorf("invalid interval")
		}
		number, err := strconv.ParseInt(parts[position], 10, 32)
		if err != nil {
			return Interval{}, fmt.Errorf("invalid interval component")
		}
		switch strings.TrimSuffix(parts[position+1], "s") {
		case "mon":
			value.Months = int32(number)
		case "day":
			value.Days = int32(number)
		default:
			return Interval{}, fmt.Errorf("invalid interval unit")
		}
		position += 2
	}
	if len(parts) == 0 {
		return Interval{}, fmt.Errorf("invalid interval")
	}
	return value, nil
}

func parseWireIntervalClock(source string) (int64, error) {
	negative := strings.HasPrefix(source, "-")
	if negative || strings.HasPrefix(source, "+") {
		source = source[1:]
	}
	parts := strings.Split(source, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid interval clock")
	}
	hours, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid interval hours")
	}
	minutes, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || minutes < 0 || minutes > 59 {
		return 0, fmt.Errorf("invalid interval minutes")
	}
	seconds, fraction, _ := strings.Cut(parts[2], ".")
	secondValue, err := strconv.ParseInt(seconds, 10, 64)
	if err != nil || secondValue < 0 || secondValue > 59 {
		return 0, fmt.Errorf("invalid interval seconds")
	}
	if len(fraction) > 6 {
		return 0, fmt.Errorf("interval exceeds microsecond precision")
	}
	fraction += strings.Repeat("0", 6-len(fraction))
	microseconds, err := strconv.ParseInt(fraction, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid interval fraction")
	}
	limit := uint64(1<<63 - 1)
	if negative {
		limit++
	}
	remainder := uint64(minutes*postgresMicrosecondsPerMinute + secondValue*postgresMicrosecondsPerSecond + microseconds)
	if hours > (limit-remainder)/uint64(postgresMicrosecondsPerHour) {
		return 0, fmt.Errorf("interval is out of range")
	}
	magnitude := hours*uint64(postgresMicrosecondsPerHour) + remainder
	if negative {
		if magnitude == uint64(1)<<63 {
			return int64(-1 << 63), nil
		}
		return -int64(magnitude), nil
	}
	return int64(magnitude), nil
}

func formatWireInterval(value Interval) string {
	parts := make([]string, 0, 3)
	if value.Months != 0 {
		parts = append(parts, fmt.Sprintf("%d mons", value.Months))
	}
	if value.Days != 0 {
		parts = append(parts, fmt.Sprintf("%d days", value.Days))
	}
	if value.Microseconds != 0 || len(parts) == 0 {
		negative := value.Microseconds < 0
		absolute := uint64(value.Microseconds)
		if negative {
			absolute = uint64(-(value.Microseconds + 1)) + 1
		}
		hours := absolute / uint64(postgresMicrosecondsPerHour)
		absolute %= uint64(postgresMicrosecondsPerHour)
		minutes := absolute / uint64(postgresMicrosecondsPerMinute)
		absolute %= uint64(postgresMicrosecondsPerMinute)
		seconds := absolute / uint64(postgresMicrosecondsPerSecond)
		microseconds := absolute % uint64(postgresMicrosecondsPerSecond)
		prefix := ""
		if negative {
			prefix = "-"
		}
		clock := fmt.Sprintf("%s%02d:%02d:%02d", prefix, hours, minutes, seconds)
		if microseconds != 0 {
			clock += "." + strings.TrimRight(fmt.Sprintf("%06d", microseconds), "0")
		}
		parts = append(parts, clock)
	}
	return strings.Join(parts, " ")
}
