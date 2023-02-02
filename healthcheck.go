// Package healthcheck provides some types and functions to implement liveness
// and readyness checks based on HTTP probes.
package healthcheck

import (
	"context"
	"encoding/json"
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
