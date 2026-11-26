package executor

import "encoding/json/v2"

// InputText renders an input value for a pipeline, which takes text only: a string as it is, no
// value as the empty string, and every other value as JSON.
func InputText(value any) (string, error) {
	switch v := value.(type) {
	case nil:
		return "", nil
	case string:
		return v, nil
	}
	encoded, err := json.Marshal(value)
	return string(encoded), err
}
