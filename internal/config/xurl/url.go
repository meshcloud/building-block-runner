package xurl

import (
	"encoding"
	"fmt"
	"net/url"
	"strings"
)

var (
	_ encoding.TextUnmarshaler = &URL{}
	_ encoding.TextMarshaler   = URL{}
)

type URL struct {
	*url.URL
}

func MustParse(s string) (result URL) {
	if err := result.UnmarshalText([]byte(s)); err != nil {
		panic(err)
	}
	return
}

func (u *URL) UnmarshalText(text []byte) (err error) {
	if u.URL, err = url.ParseRequestURI(string(text)); err != nil {
		return
	}
	if u.Scheme != "http" && u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("URL '%s' must be absolute and start with 'http://' or 'https://'", text)
	}
	u.Host = strings.ToLower(u.Host)
	if u.Path == "/" {
		u.Path = ""
	}
	return
}

func (u URL) MarshalText() ([]byte, error) {
	if u.URL == nil {
		return nil, nil
	}
	return []byte(u.String()), nil
}

func (u URL) String() string {
	if u.URL == nil {
		return ""
	}
	return u.URL.String()
}
