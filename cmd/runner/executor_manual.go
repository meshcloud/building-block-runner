//go:build !k8s && (manual || !(tf || github || gitlab || azuredevops))

package main

import _ "github.com/meshcloud/building-block-runner/internal/executor/manual"
