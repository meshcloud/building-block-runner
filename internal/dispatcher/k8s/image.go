//go:build k8s

package k8s

import (
	"path"
	"strings"
)

func withDefaultTag(image, tag string) string {
	if tag == "" || strings.Contains(image, "@") || strings.Contains(path.Base(image), ":") {
		return image
	}
	return image + ":" + tag
}
