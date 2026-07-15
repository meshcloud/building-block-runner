package dispatch

import (
	"errors"
	"net/http"
	"testing"

	"github.com/meshcloud/building-block-runner/internal/meshapi"
)

func TestStandaloneClaimClassifier(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want ClaimOutcome
	}{
		{"404 is the idle no-run outcome", meshapi.HttpError{StatusCode: http.StatusNotFound}, OutcomeNoRun},
		{"409 is logged no-run", meshapi.HttpError{StatusCode: http.StatusConflict}, OutcomeNoRunLogged},
		{"other transport error is logged no-run", errors.New("boom"), OutcomeNoRunLogged},
		{"500 is logged no-run", meshapi.HttpError{StatusCode: 500}, OutcomeNoRunLogged},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := StandaloneClaimClassifier(c.err); got != c.want {
				t.Errorf("StandaloneClaimClassifier(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}
