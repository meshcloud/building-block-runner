package http

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"log/slog"
	gohttp "net/http"
	"net/url"
	"os"
	"reflect"
	"slices"
	"time"
)

var sharedClient = func() (client *gohttp.Client) {
	client = &gohttp.Client{Transport: newTransport(1 * time.Minute)}
	RetryOptions{
		// Rides out a meshStack backend restart, whose gateway answers 503 for two to three minutes.
		MaxRetries: 12,
		Backoff:    ExponentialBackoff{MinWait: 1 * time.Second, MaxWait: 30 * time.Second},
	}.ApplyTo(client)
	return
}()

func newTransport(silenceTimeout time.Duration) gohttp.RoundTripper {
	transport := gohttp.DefaultTransport.(*gohttp.Transport).Clone() //nolint:forcetypeassert // net/http declares it a *Transport
	transport.ResponseHeaderTimeout = silenceTimeout
	return stallGuard{Next: transport, Timeout: silenceTimeout}
}

type Client struct {
	*gohttp.Client

	UserAgent UserAgent
}

func NewClient(userAgent UserAgent) Client {
	if err := userAgent.Validate(); err != nil {
		panic(err)
	}
	return Client{sharedClient, userAgent}
}

// DoRequest returns the body as it came for an R of []byte, and parses it as JSON otherwise.
func (c Client) DoRequest[R any](ctx context.Context, method string, url *url.URL, options ...RequestOption) (result R, err error) {
	var body []byte
	body, err = c.doRequest(ctx, method, url, options)
	if err != nil {
		return
	}
	if raw, ok := any(&result).(*[]byte); ok {
		*raw = body
		return
	}
	if len(body) == 0 {
		// Only DoRequest[any] expects no content; any other caller would read a zero result as an answer.
		if t := reflect.TypeFor[R](); t.Kind() == reflect.Interface && t.NumMethod() == 0 {
			return
		}
		err = fmt.Errorf("unexpected empty response body from %s %s", method, url)
		return
	}
	if err = json.Unmarshal(body, &result); err != nil {
		err = fmt.Errorf("parsing response body as JSON failed: %w", err)
	}
	return
}

// DoStreamingRequest copies a 2xx body to w as it arrives.
func (c Client) DoStreamingRequest(ctx context.Context, method string, url *url.URL, w io.Writer, options ...RequestOption) error {
	res, err := c.send(ctx, method, url, options)
	if err != nil {
		return err
	}
	defer func() {
		_ = res.Body.Close()
	}()
	if !isSuccess(res.StatusCode) {
		_, err = c.readBodyAndCheckSuccess(ctx, res)
		return err
	}
	written, err := io.Copy(w, res.Body)
	slog.DebugContext(ctx, "response", "status", res.StatusCode, "body", fmt.Sprintf("<streamed,len=%d>", written))
	return err
}

func (c Client) doRequest(ctx context.Context, method string, url *url.URL, options []RequestOption) ([]byte, error) {
	res, err := c.send(ctx, method, url, options)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = res.Body.Close()
	}()
	return c.readBodyAndCheckSuccess(ctx, res)
}

func (c Client) send(ctx context.Context, method string, url *url.URL, options []RequestOption) (*gohttp.Response, error) {
	options = slices.Insert(options, 0, withHeader("User-Agent", c.UserAgent.String()))
	opts := requestOptions{}
	for _, option := range options {
		option(&opts)
	}
	req, err := c.buildRequest(ctx, method, url, opts)
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}

func isSuccess(statusCode int) bool {
	return statusCode >= 200 && statusCode <= 299
}

func (c Client) readBodyAndCheckSuccess(ctx context.Context, res *gohttp.Response) ([]byte, error) {
	responseBody, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("cannot read response body, status code %d: %w", res.StatusCode, err)
	}
	slog.DebugContext(ctx, "response", "status", res.StatusCode, "body", loggedBody{bytes.NewBuffer(responseBody)})

	if isSuccess(res.StatusCode) {
		return responseBody, nil
	}

	return responseBody, Error{
		StatusCode:   res.StatusCode,
		ResponseBody: responseBody,
	}
}

func (c Client) buildRequest(ctx context.Context, method string, url *url.URL, opts requestOptions) (*gohttp.Request, error) {
	var requestBody io.Reader
	if opts.requestPayload != nil {
		requestBodyData, err := opts.requestPayload()
		if err != nil {
			return nil, fmt.Errorf("cannot build request body data: %w", err)
		}
		requestBody = bytes.NewBuffer(requestBodyData)
	}

	if opts.retryable {
		ctx = context.WithValue(ctx, retryableKey{}, true)
	}

	req, err := gohttp.NewRequestWithContext(ctx, method, url.String(), requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	if opts.bodyFile != "" {
		if err := setFileBody(req, opts.bodyFile); err != nil {
			return nil, err
		}
		requestBody = req.Body
	}
	for _, modifier := range opts.requestModifiers {
		if err := modifier(req); err != nil {
			return nil, err
		}
	}
	slog.DebugContext(ctx, "request", "url", req.URL.String(), "method", req.Method, "headers", loggedHeaders(req.Header), "body", loggedBody{requestBody})
	return req, nil
}

// setFileBody streams the file, and reopens it when a retry replays the request.
func setFileBody(req *gohttp.Request, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	open := func() (io.ReadCloser, error) {
		return os.Open(path) //nolint:gosec // the caller names the file it uploads
	}
	if req.Body, err = open(); err != nil {
		return err
	}
	req.GetBody = open
	req.ContentLength = info.Size()
	return nil
}
