package http

import (
	"fmt"
	gohttp "net/http"
)

type Error struct {
	StatusCode   int
	ResponseBody []byte
}

func (e Error) Error() string {
	return fmt.Sprintf("http error %d, response '%s'", e.StatusCode, string(e.ResponseBody))
}

func (e Error) IsUnauthorized() bool {
	return e.StatusCode == gohttp.StatusUnauthorized
}

func (e Error) IsNotFound() bool {
	return e.StatusCode == gohttp.StatusNotFound
}

func (e Error) IsConflict() bool {
	return e.StatusCode == gohttp.StatusConflict
}
