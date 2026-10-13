package xtime

import (
	"strconv"
	"time"
)

type DefaultUnit interface{ Minute | Second }
type Minute struct{}
type Second struct{}

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

func (d *Duration[U]) UnmarshalText(text []byte) (err error) {
	if numberWithoutUnit, err := strconv.Atoi(string(text)); err == nil {
		switch any(d.defaultUnit).(type) {
		case Minute:
			d.Duration = time.Duration(numberWithoutUnit) * time.Minute
			return nil
		case Second:
			d.Duration = time.Duration(numberWithoutUnit) * time.Second
			return nil
		default:
			// fall through ParseDuration
		}
	}
	d.Duration, err = time.ParseDuration(string(text))
	return
}
