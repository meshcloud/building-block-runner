//go:build k8s

package k8s

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const errorTypeServiceAccountCreation = "creation_error"

var (
	serviceAccountsCreated = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "run_controller_service_accounts_created_total",
		Help: "Total number of Kubernetes service accounts created for workload identity",
	}, []string{"controller_uuid"})
	serviceAccountErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "run_controller_service_account_creation_errors_total",
		Help: "Total number of errors while creating Kubernetes service accounts",
	}, []string{"controller_uuid", "error_type"})
)
