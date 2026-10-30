//go:build !k8s

package process

import "github.com/meshcloud/building-block-runner/v2/internal/dispatcher"

func init() { dispatcher.Current = New }
