// Package healthcheck provides some types and functions to implement liveness
// and readyness checks based on HTTP probes.
package healthcheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"
	"strconv"
	"sync"

	"golang.org/x/sync/errgroup"
)

// Check defines the interface for custom readyness checks.
type Check interface {
	// Check is called to execute the check. Any non-nil return value
	// is considered a check failure incl. context deadlines.
	Check(context.Context) error
}

// CheckFunc is a convenience type to implement Check using a bare function.
type CheckFunc func(context.Context) error

func (f CheckFunc) Check(ctx context.Context) error { return f(ctx) }

// --

var ErrURLCheckFailed = errors.New("URL check failed")

// CheckURL creates a Check that checks url for a status code < 400. The returned
// check uses http.DefaultClient to issue the HTTP request.
// Use CheckHTTPResponse when custom handling is needed.
func CheckURL(url string) Check {
	return CheckHTTPResponse(http.MethodGet, url, nil)
}

// CheckHTTPResponse creates a Check that issues a HTTP request with method to
// url using client. The check reports an error if either the request fails or
// the received status code is >= 400 (bad request).
// If client is nil http.DefaultClient is used.
func CheckHTTPResponse(method, url string, client *http.Client) Check {
	if client == nil {
		client = http.DefaultClient
	}

	return CheckFunc(func(ctx context.Context) error {
		req, err := http.NewRequestWithContext(ctx, method, url, nil)
		if err != nil {
			return fmt.Errorf("%w: failed to create http request for %s %s: %s", ErrURLCheckFailed, method, url, err)
		}

		res, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("%w: failed to issue http request for %s %s: %s", ErrURLCheckFailed, method, url, err)
		}

		if res.StatusCode >= http.StatusBadRequest {
			return fmt.Errorf("%w: got failing status code for %s %s: %d", ErrURLCheckFailed, method, url, res.StatusCode)
		}

		return nil
	})
}

var ErrPingCheckFailed = errors.New("ping check failed")

// Pinger defines the interface for connection types that support pinging the
// remote endpoint to learn about its liveness. The method name is chosen to
// make a value of type *sql.DB satisfy this interface without any adaption.
type Pinger interface {
	// PingContext pings the remote endpoint. It returns nil if the endpoint is
	// healthy, a non-nil error otherwise.
	PingContext(context.Context) error
}

// CheckPing creates a Check that calls PingContext to check for connectivity.
// This method can directly be used on a *sql.DB.
func CheckPing(pinger Pinger) Check {
	return CheckFunc(func(ctx context.Context) error {
		if err := pinger.PingContext(ctx); err != nil {
			return fmt.Errorf("%w: %s", ErrPingCheckFailed, err)
		}
		return nil
	})
}

// --

// ErrorLogFunc defines a type for a function to log errors that occured during ready check execution.
// err is the error returned by the check function.
type ErrorLogFunc func(err error)

// --

var (
	// Configures the final path element of the URL serving the liveness check. Changes to this variable will only take effect when done before calling New.
	LivePath = "/livez"
	// Configures the final path element of the URL serving the readyness check. Changes to this variable will only take effect when done before calling New.
	ReadyPath = "/readyz"
	// Configures the final path element of the URL serving the info endpoint. Changes to this variable will only take effect when done before calling New.
	InfoPath = "/infoz"
)

// Handler implements liveness and readyness checking.
type Handler struct {
	errorLogFunc ErrorLogFunc
	checks       []Check
	lock         sync.RWMutex
	mux          http.ServeMux
	infoPayload  []byte
}

// New creates a new Handler ready to use. The Handler must be
// mounted on some HTTP path (i.e. on a http.ServeMux) to receive
// requests.
func New(errorLogFunc ErrorLogFunc) *Handler {
	h := &Handler{
		errorLogFunc: errorLogFunc,
		mux:          *http.NewServeMux(),
	}

	h.mux.HandleFunc(LivePath, h.handleLive)
	h.mux.HandleFunc(ReadyPath, h.handleReady)

	return h
}

// AddCheckFunc registers c as another readyness check.
func (h *Handler) AddCheckFunc(c CheckFunc) {
	h.AddCheck(CheckFunc(c))
}

// AddCheck registers c as another readyness check.
func (h *Handler) AddCheck(c Check) {
	h.lock.Lock()
	defer h.lock.Unlock()

	h.checks = append(h.checks, c)
}

// EnableInfo enables an info endpoint that outputs version information and additional details.
func (h *Handler) EnableInfo(infoData map[string]any) {
	if infoData == nil {
		infoData = make(map[string]any)
	}

	info, ok := debug.ReadBuildInfo()
	if ok {
		infoData["version"] = info.Main.Version
		settings := make(map[string]any)
		for _, s := range info.Settings {
			settings[s.Key] = s.Value
		}
		infoData["build_settings"] = settings
	}

	var err error
	h.infoPayload, err = json.Marshal(infoData)
	if err != nil {
		panic(err)
	}

	h.mux.HandleFunc(InfoPath, h.handleInfo)
}

// ServeHTTP dispatches and executes health checks.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) handleLive(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleReady(w http.ResponseWriter, r *http.Request) {
	h.lock.RLock()
	defer h.lock.RUnlock()

	eg, ctx := errgroup.WithContext(r.Context())

	for _, c := range h.checks {
		c := c
		eg.Go(func() error { return c.Check(ctx) })
	}

	if err := eg.Wait(); err != nil {
		if h.errorLogFunc != nil {
			h.errorLogFunc(err)
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.Header().Set("Content-Lengt", strconv.Itoa(len(h.infoPayload)))
	w.WriteHeader(http.StatusOK)
	w.Write(h.infoPayload)
}
