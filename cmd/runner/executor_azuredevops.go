//go:build !k8s && (azuredevops || !(tf || manual || github || gitlab))

package main

import _ "github.com/meshcloud/building-block-runner/internal/executor/azuredevops"
