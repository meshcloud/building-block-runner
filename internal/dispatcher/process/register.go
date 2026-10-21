//go:build !k8s

package process

import "github.com/meshcloud/building-block-runner/internal/dispatcher"

func init() { dispatcher.Current = New }
