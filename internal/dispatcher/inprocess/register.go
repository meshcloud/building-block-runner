//go:build !k8s

package inprocess

import "github.com/meshcloud/building-block-runner/internal/dispatcher"

func init() {
	dispatcher.Current = New
	dispatcher.DefaultForTypes = defaultForTypes
}
