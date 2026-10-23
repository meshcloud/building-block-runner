//go:build !k8s && (gitlab || !(tf || manual || github || azuredevops))

package main

import _ "github.com/meshcloud/building-block-runner/v2/internal/executor/gitlab"
