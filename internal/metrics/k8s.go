//go:build k8s

package metrics

import (
	"maps"
	"slices"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"google.golang.org/protobuf/proto"
)

// The Kubernetes run-controller exposed its metrics under these names, which existing dashboards and alerts still use.
const (
	legacyMetricPrefix = "run_controller_"
	legacyLabelUuid    = "controller_uuid"
)

type legacyMetric struct {
	from string
	// relabel changes the labels of a series in place and returns false to leave the series out.
	relabel func(labels map[string]string) bool
}

var legacyMetrics = map[string]legacyMetric{
	"runs_fetch_errors_total":     {from: "claim_errors_total", relabel: withErrorType("api_error")},
	"runs_fetch_duration_seconds": {from: "claim_duration_seconds"},
	"jobs_created_total":          {from: "runs_started_total"},
	"job_creation_errors_total": {from: "start_errors_total", relabel: func(labels map[string]string) bool {
		switch labels[labelErrorType] {
		case ErrorTypeDecryption:
			return false
		case ErrorTypeStart:
			labels[labelErrorType] = "job_creation_error"
		}
		return true
	}},
	"job_creation_duration_seconds": {from: "start_duration_seconds"},
	"decryption_errors_total": {from: "start_errors_total", relabel: func(labels map[string]string) bool {
		errorType := labels[labelErrorType]
		delete(labels, labelErrorType)
		return errorType == ErrorTypeDecryption
	}},
	"runner_registration_success_total":     {from: "registrations_total"},
	"runner_registration_errors_total":      {from: "registration_errors_total", relabel: withErrorType("put_error")},
	"service_accounts_created_total":        {from: "service_accounts_created_total"},
	"service_account_creation_errors_total": {from: "service_account_errors_total", relabel: withErrorType("creation_error")},
}

func withErrorType(errorType string) func(map[string]string) bool {
	return func(labels map[string]string) bool {
		labels[labelErrorType] = errorType
		return true
	}
}

var gatherer prometheus.Gatherer = legacyGatherer{registry}

// legacyGatherer adds the run-controller metrics, which it derives from the new ones.
type legacyGatherer struct {
	prometheus.Gatherer
}

func (g legacyGatherer) Gather() ([]*dto.MetricFamily, error) {
	families, err := g.Gatherer.Gather()
	byName := make(map[string]*dto.MetricFamily, len(families))
	for _, family := range families {
		byName[family.GetName()] = family
	}
	runnerUuid := infoLabel(byName[infoName], labelUuid)
	for name, legacy := range legacyMetrics {
		family, ok := byName[metricPrefix+legacy.from]
		if !ok {
			continue
		}
		copied := proto.CloneOf(family)
		copied.Name = new(legacyMetricPrefix + name)
		copied.Metric = slices.DeleteFunc(copied.GetMetric(), func(metric *dto.Metric) bool {
			labels := make(map[string]string, len(metric.GetLabel())+1)
			for _, label := range metric.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if legacy.relabel != nil && !legacy.relabel(labels) {
				return true
			}
			labels[legacyLabelUuid] = runnerUuid
			metric.Label = nil
			for _, labelName := range slices.Sorted(maps.Keys(labels)) {
				metric.Label = append(metric.Label, &dto.LabelPair{Name: new(labelName), Value: new(labels[labelName])})
			}
			return false
		})
		if len(copied.GetMetric()) > 0 {
			families = append(families, copied)
		}
	}
	slices.SortFunc(families, func(a, b *dto.MetricFamily) int { return strings.Compare(a.GetName(), b.GetName()) })
	return families, err
}

func infoLabel(info *dto.MetricFamily, labelName string) string {
	for _, metric := range info.GetMetric() {
		for _, label := range metric.GetLabel() {
			if label.GetName() == labelName {
				return label.GetValue()
			}
		}
	}
	return ""
}
