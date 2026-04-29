package tfrun

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_prependToPathEnvironmentVariable(t *testing.T) {
	pathSeparator := string(os.PathListSeparator)

	type args struct {
		environ []string
		paths   []string
	}
	tests := []struct {
		name string
		args args
		want []string
	}{
		{"empty", args{}, nil},
		{"empty path", args{paths: []string{""}}, nil},
		{"single path", args{paths: []string{"/one-path"}}, []string{"PATH=/one-path"}},
		{
			"two paths, whitespace one ignored",
			args{paths: []string{"/one-path", "\t", "another-path"}},
			[]string{"PATH=" + strings.Join([]string{"/one-path", "another-path"}, pathSeparator)},
		},
		{
			"single path prepended",
			args{environ: []string{"PATH=" + strings.Join([]string{"/one-path", "another-path"}, pathSeparator)}, paths: []string{"/prepended-path"}},
			[]string{"PATH=" + strings.Join([]string{"/prepended-path", "/one-path", "another-path"}, pathSeparator)},
		},
		{
			"two paths prepended to empty PATH",
			args{environ: []string{"PATH="}, paths: []string{"/prepended-path1", "/prepended-path2"}},
			[]string{"PATH=" + strings.Join([]string{"/prepended-path1", "/prepended-path2"}, pathSeparator)},
		},
		{"single path with other environments", args{environ: []string{"OTHER=stuff"}, paths: []string{"/path1"}}, []string{"OTHER=stuff", "PATH=/path1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, prependToPathEnvironmentVariable(tt.args.environ, tt.args.paths...), "prependToPathEnvironmentVariable(%v, %v)", tt.args.environ, tt.args.paths)
		})
	}
}

func Test_buildScriptEnvironmentVariables(t *testing.T) {
	pathSeparator := regexp.QuoteMeta(string(os.PathListSeparator))

	actual := buildScriptEnvironmentVariables("/some/path/to/terraform", "/some/path/to/user-message.txt")
	assert.Contains(t, actual, "MESHSTACK_USER_MESSAGE=/some/path/to/user-message.txt")
	foundPrependedPath := false
	for _, envKeyValue := range actual {
		matched, err := regexp.MatchString("^PATH=/some/path/to/terraform($|"+pathSeparator+")", envKeyValue)
		require.NoError(t, err)
		if matched {
			foundPrependedPath = true
		}
	}
	assert.True(t, foundPrependedPath)
}
