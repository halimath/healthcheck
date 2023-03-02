// Package healthcheck provides types and functions to implement liveness
// and readyness checks based on HTTP probes. The package provides a
// http.Handler that can be mounted using to a running server. Client code can
// register checks to be executed when the readyness endpoint is invoked.
// The package also provides ready to use checks for HTTP endpoints and SQL
// databases.
//
// The handler also reports version information of the running application. This
// is an opt-in feature disabled by default. The version info will be gathered
// using the runtime/debug and can be enhanced with custom fields.
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
	"time"

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
			return fmt.Errorf("%w: failed to create http request for %s %s: %s",
				ErrURLCheckFailed, method, url, err)
		}

		res, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("%w: failed to issue http request for %s %s: %s",
				ErrURLCheckFailed, method, url, err)
		}

		if res.StatusCode >= http.StatusBadRequest {
			return fmt.Errorf("%w: got failing status code for %s %s: %d",
				ErrURLCheckFailed, method, url, res.StatusCode)
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

// ErrorLogger defines a type for a function to log errors that occured during
// ready check execution. err is the error returned by the check function.
type ErrorLogger func(err error)

// --

var (
	// Configures the final path element of the URL serving the liveness check.
	// Changes to this variable will only take effect when done before calling New.
	LivePath = "/livez"
	// Configures the final path element of the URL serving the readyness check.
	// Changes to this variable will only take effect when done before calling New.
	ReadyPath = "/readyz"
	// Configures the final path element of the URL serving the info endpoint.
	// Changes to this variable will only take effect when done before calling New.
	InfoPath = "/infoz"

	// Default timeout to apply to readyness checks
	DefaultReadynessCheckTimeout = 10 * time.Second
)

// Option defines a function type used to customize the provided Handler.
type Option func(*Handler)

// WithErrorLogger creates an Option that sets Handler.ErrorLogger to l.
func WithErrorLogger(l ErrorLogger) Option {
	return func(h *Handler) {
		h.ErrorLogger = l
	}
}

// WithReadynessTimeout creates an Option that sets Handler.ReadynessTimeout to
// t.
func WithReadynessTimeout(t time.Duration) Option {
	return func(h *Handler) {
		h.ReadynessTimeout = t
	}
}

// Handler implements liveness and readyness checking.
type Handler struct {
	ErrorLogger      ErrorLogger
	ReadynessTimeout time.Duration

	checks      []Check
	lock        sync.RWMutex
	mux         http.ServeMux
	infoPayload []byte
}

// New creates a new Handler ready to use. The Handler must be
// mounted on some HTTP path (i.e. on a http.ServeMux) to receive
// requests.
func New(opts ...Option) *Handler {
	h := &Handler{
		mux:              *http.NewServeMux(),
		ReadynessTimeout: DefaultReadynessCheckTimeout,
	}

	for _, opt := range opts {
		if opt != nil {
			opt(h)
		}
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

// ExecuteReadyChecks executes all readyness checks in parallel. It reports the
// first error hit or nil if all checks pass. Every check is executed with a
// timeout configured for the handler (if any).
func (h *Handler) ExecuteReadyChecks(ctx context.Context) error {
	h.lock.RLock()
	defer h.lock.RUnlock()

	if h.ReadynessTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, h.ReadynessTimeout)
		defer cancel()
	}

	eg, ctx := errgroup.WithContext(ctx)

	for _, c := range h.checks {
		c := c
		eg.Go(func() error { return c.Check(ctx) })
	}

	if err := eg.Wait(); err != nil {
		if h.ErrorLogger != nil {
			h.ErrorLogger(err)
		}
		return err
	}

	return nil
}

// EnableInfo enables an info endpoint that outputs version information and
// additional details.
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
	if err := h.ExecuteReadyChecks(r.Context()); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(h.infoPayload)))
	w.WriteHeader(http.StatusOK)
	w.Write(h.infoPayload)
}
