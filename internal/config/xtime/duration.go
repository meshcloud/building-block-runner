package xtime

import (
	"strconv"
	"time"
)

type (
	DefaultUnit interface{ Minute | Second }
	Minute      struct{}
	Second      struct{}
)

type Duration[U DefaultUnit] struct {
	time.Duration

	defaultUnit U
}

func Minutes(n int) Duration[Minute] {
	return Duration[Minute]{Duration: time.Duration(n) * time.Minute}
}

func Seconds(n int) Duration[Second] {
	return Duration[Second]{Duration: time.Duration(n) * time.Second}
}

//goland:noinspection GoMixedReceiverTypes // UnmarshalText sets the value, so it needs a pointer.
func (d *Duration[U]) UnmarshalText(text []byte) (err error) {
	if numberWithoutUnit, atoiErr := strconv.Atoi(string(text)); atoiErr == nil {
		switch any(d.defaultUnit).(type) {
		case Minute:
			d.Duration = time.Duration(numberWithoutUnit) * time.Minute
			return nil
		case Second:
			d.Duration = time.Duration(numberWithoutUnit) * time.Second
			return nil
		}
	}
	d.Duration, err = time.ParseDuration(string(text))
	return
}

//goland:noinspection GoMixedReceiverTypes // a value receiver marshals a Duration field of a struct value too.
func (d Duration[U]) MarshalText() ([]byte, error) {
	return []byte(d.String()), nil
}
