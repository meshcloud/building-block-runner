//go:build !k8s && (azuredevops || !(tf || manual || github || gitlab))

package main

import _ "github.com/meshcloud/building-block-runner/v2/internal/executor/azuredevops"
