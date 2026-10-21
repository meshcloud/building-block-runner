//go:build k8s

package k8s

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilnet "k8s.io/apimachinery/pkg/util/net"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
)

var createBackoff = wait.Backoff{Steps: 5, Duration: 200 * time.Millisecond, Factor: 2, Jitter: 0.1}

func createRetrying[T any](ctx context.Context, create func(context.Context, T, metav1.CreateOptions) (T, error), object T) (T, error) {
	var created T
	err := retry.OnError(createBackoff, isTransient, func() (err error) {
		created, err = create(ctx, object, metav1.CreateOptions{})
		return err
	})
	return created, err
}

func isTransient(err error) bool {
	if statusErr, ok := errors.AsType[*apierrors.StatusError](err); ok {
		code := statusErr.ErrStatus.Code
		return code == http.StatusTooManyRequests || code >= http.StatusInternalServerError
	}
	if netErr, ok := errors.AsType[net.Error](err); ok && netErr.Timeout() {
		return true
	}
	return utilnet.IsConnectionRefused(err) || utilnet.IsConnectionReset(err) || utilnet.IsProbableEOF(err)
}
