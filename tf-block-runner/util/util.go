package util

// A Must turns an error to a panic.
func Must[T any](v T, err error) T {
	if err == nil {
		return v
	}
	panic(err)
}
