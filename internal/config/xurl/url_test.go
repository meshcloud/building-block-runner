package xurl

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_URL_AcceptsOnlyAbsoluteHttpUrls(t *testing.T) {
	for _, valid := range []string{"http://host.minikube.internal:8080", "https://meshstack.example.com/", "http://localhost:8300"} {
		var u URL
		require.NoError(t, u.UnmarshalText([]byte(valid)), valid)
	}
	assert.Equal(t, "http://host.example.com", MustParse("http://HOST.example.com/").String())
	assert.Empty(t, URL{}.String())

	for _, invalid := range []string{"", "host.example.com", "/relative", "ftp://host.example.com", "http://"} {
		var u URL
		assert.Error(t, u.UnmarshalText([]byte(invalid)), invalid)
	}
}
