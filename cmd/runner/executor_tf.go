//go:build !k8s && (tf || !manual)

package main

import _ "github.com/meshcloud/building-block-runner/internal/executor/tf"
