package tfrun

import (
	"fmt"
	"testing"

	"github.com/PaesslerAG/jsonpath"
	"github.com/stretchr/testify/assert"
)

func assertJsonEqual(t *testing.T, jsonRoot any, path string, expected any) {
	value, err := jsonpath.Get(path, jsonRoot)
	if err != nil {
		assert.Fail(t, err.Error())
	}

	assert.Equal(t, expected, value)
}

func assertJsonExists(t *testing.T, jsonRoot any, path string) {
	value, err := jsonpath.Get(path, jsonRoot)
	if err != nil {
		assert.Fail(t, err.Error())
	}

	assert.True(t, value != nil)
}

func assertJsonNotExists(t *testing.T, jsonRoot any, path string) {
	val, err := jsonpath.Get(path, jsonRoot)
	if val != nil && err == nil {
		assert.Fail(t, fmt.Sprintf("value at '%s' exists", path))
	}

	if err != nil {
		assert.Contains(t, err.Error(), "unknown key")
	} else {
		assert.Nil(t, val)
	}
}

func assertJsonLen(t *testing.T, jsonRoot any, path string, length int) {
	value, err := jsonpath.Get(path, jsonRoot)
	if err != nil {
		assert.Fail(t, err.Error())
	}

	assert.Len(t, value, length)
}
