//go:build !k8s && (github || !(tf || manual || gitlab || azuredevops))

package main

import _ "github.com/meshcloud/building-block-runner/internal/executor/github"
