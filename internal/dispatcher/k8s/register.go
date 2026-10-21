//go:build k8s

package k8s

import "github.com/meshcloud/building-block-runner/internal/dispatcher"

func init() { dispatcher.Current = New }
