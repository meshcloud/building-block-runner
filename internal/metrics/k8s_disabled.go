//go:build !k8s

package metrics

import "github.com/prometheus/client_golang/prometheus"

var gatherer prometheus.Gatherer = registry
