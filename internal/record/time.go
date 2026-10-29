package record

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// Precision is how exactly an instant is known.
type Precision string

const (
	Second Precision = "second"
	Minute Precision = "minute"
	Day    Precision = "day"
	Month  Precision = "month"
	Year   Precision = "year"
)

func (p Precision) valid() bool {
	switch p {
	case Second, Minute, Day, Month, Year:
		return true
	}
	return false
}

// Instant is a point in time and how exactly it is known. "Sometime in March"
// is an Instant at 1 March with Month precision.
type Instant struct {
	At        time.Time
	Precision Precision
}

// Exact is an instant known to the second.
func Exact(t time.Time) Instant { return Instant{At: t, Precision: Second} }

// Until is the end of the span the instant could fall in: the last moment of
// March for an instant known only to the month.
func (i Instant) Until() time.Time {
	switch i.Precision {
	case Minute:
		return i.At.Add(time.Minute)
	case Day:
		return i.At.AddDate(0, 0, 1)
	case Month:
		return i.At.AddDate(0, 1, 0)
	case Year:
		return i.At.AddDate(1, 0, 0)
	}
	return i.At
}

func (i Instant) MarshalJSON() ([]byte, error) {
	at := i.At.Format(time.RFC3339Nano)
	if i.Precision == "" || i.Precision == Second {
		return json.Marshal(at)
	}
	return json.Marshal(struct {
		At        string    `json:"at"`
		Precision Precision `json:"precision"`
	}{at, i.Precision})
}

func (i *Instant) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) > 0 && data[0] == '"' {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		at, err := time.Parse(time.RFC3339Nano, text)
		if err != nil {
			return fmt.Errorf("time %q is not RFC 3339 with an offset", text)
		}
		*i = Instant{At: at, Precision: Second}
		return nil
	}
	var written struct {
		At        string    `json:"at"`
		Precision Precision `json:"precision"`
	}
	if err := json.Unmarshal(data, &written); err != nil {
		return err
	}
	at, err := time.Parse(time.RFC3339Nano, written.At)
	if err != nil {
		return fmt.Errorf("time %q is not RFC 3339 with an offset", written.At)
	}
	if written.Precision == "" {
		written.Precision = Second
	}
	if !written.Precision.valid() {
		return fmt.Errorf("precision %q is not one of second, minute, day, month, year", written.Precision)
	}
	*i = Instant{At: at, Precision: written.Precision}
	return nil
}
