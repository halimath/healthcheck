package healthcheck

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	. "github.com/halimath/expect-go"
	. "github.com/halimath/fixture"
)

type handlerFixture struct {
	handler *Handler
}

func (h *handlerFixture) BeforeAll(t *testing.T) error {
	h.handler = New()
	return nil
}

func (h *handlerFixture) LivenessRequest() int {
	r := httptest.NewRequest(http.MethodGet, LivePath, nil)
	w := httptest.NewRecorder()

	h.handler.ServeHTTP(w, r)

	return w.Result().StatusCode
}

func (h *handlerFixture) ReadynessRequest() int {
	r := httptest.NewRequest(http.MethodGet, ReadyPath, nil)
	w := httptest.NewRecorder()

	h.handler.ServeHTTP(w, r)

	return w.Result().StatusCode
}

func (h *handlerFixture) InfoRequest() ([]byte, int) {
	r := httptest.NewRequest(http.MethodGet, InfoPath, nil)
	w := httptest.NewRecorder()

	h.handler.ServeHTTP(w, r)

	return w.Body.Bytes(), w.Result().StatusCode
}

func TestHandler_ExecuteReadyChecks(t *testing.T) {
	With(t, new(handlerFixture)).
		Run("noCheck", func(t *testing.T, f *handlerFixture) {
			err := f.handler.ExecuteReadyChecks(context.Background())
			ExpectThat(t, err).Is(NoError())
		}).
		Run("singleSuccessfulCheck", func(t *testing.T, f *handlerFixture) {
			f.handler.AddCheckFunc(func(context.Context) error { return nil })
			err := f.handler.ExecuteReadyChecks(context.Background())
			ExpectThat(t, err).Is(NoError())
		}).
		Run("failingCheck", func(t *testing.T, f *handlerFixture) {
			want := errors.New("failed")
			f.handler.AddCheckFunc(func(context.Context) error { return want })
			err := f.handler.ExecuteReadyChecks(context.Background())
			ExpectThat(t, err).Is(Error(want))
		})
}

func TestHandler_ExecuteReadyChecks_withTimeout(t *testing.T) {
	h := New(WithReadynessTimeout(time.Millisecond))

	h.AddCheckFunc(func(ctx context.Context) error {
		t := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
			return nil
		}
	})

	err := h.ExecuteReadyChecks(context.Background())
	ExpectThat(t, err).Is(Error(context.DeadlineExceeded))
}

func TestHandler_ExecuteReadyChecks_withErrorLogger(t *testing.T) {
	var err error
	h := New(WithErrorLogger(func(e error) {
		err = e
	}))

	want := errors.New("caboom")

	h.AddCheckFunc(func(context.Context) error {
		return want
	})

	h.ExecuteReadyChecks(context.Background())
	ExpectThat(t, err).Is(Error(want))
}

func TestHandler(t *testing.T) {
	With(t, new(handlerFixture)).
		Run("liveness", func(t *testing.T, f *handlerFixture) {
			got := f.LivenessRequest()
			ExpectThat(t, got).Is(Equal(http.StatusNoContent))
		}).
		Run("readyness_successfulCheck", func(t *testing.T, f *handlerFixture) {
			f.handler.AddCheckFunc(func(context.Context) error { return nil })
			got := f.ReadynessRequest()
			ExpectThat(t, got).Is(Equal(http.StatusNoContent))
		}).
		Run("readyness_failingCheck", func(t *testing.T, f *handlerFixture) {
			f.handler.AddCheckFunc(func(context.Context) error { return errors.New("failed") })
			got := f.ReadynessRequest()
			ExpectThat(t, got).Is(Equal(http.StatusServiceUnavailable))
		}).
		Run("info_notConfigured", func(t *testing.T, f *handlerFixture) {
			_, status := f.InfoRequest()
			ExpectThat(t, status).Is(Equal(http.StatusNotFound))
		}).
		Run("info_configured", func(t *testing.T, f *handlerFixture) {
			f.handler.EnableInfo(nil)
			data, status := f.InfoRequest()
			ExpectThat(t, status).Is(Equal(http.StatusOK))

			var info map[string]any
			EnsureThat(t, json.Unmarshal(data, &info)).Is(NoError())
			ExpectThat(t, info).Is(DeepEqual(map[string]any{
				"version":        "",
				"build_settings": map[string]any{},
			}))
		})
}

func TestCheckHTTPResponse(t *testing.T) {
	f := new(HTTPServerFixture)

	f.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	f.HandleFunc("/fail", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})

	With(t, f).
		Run("CheckURL/ok", func(t *testing.T, f *HTTPServerFixture) {
			err := CheckURL(f.URL("ok")).Check(context.Background())
			ExpectThat(t, err).Is(NoError())
		}).
		Run("CheckURL/unavailable", func(t *testing.T, f *HTTPServerFixture) {
			err := CheckURL(f.URL("fail")).Check(context.Background())
			ExpectThat(t, err).Is(Error(ErrURLCheckFailed))
		}).
		Run("CheckHTTPResponse/ok", func(t *testing.T, f *HTTPServerFixture) {
			err := CheckHTTPResponse(http.MethodGet, f.URL("ok"), nil).Check(context.Background())
			ExpectThat(t, err).Is(NoError())
		}).
		Run("CheckHTTPResponse/unavailable", func(t *testing.T, f *HTTPServerFixture) {
			err := CheckHTTPResponse(http.MethodGet, f.URL("fail"), nil).Check(context.Background())
			ExpectThat(t, err).Is(Error(ErrURLCheckFailed))
		}).
		Run("CheckHTTPResponse/invalid_url", func(t *testing.T, f *HTTPServerFixture) {
			err := CheckHTTPResponse(http.MethodGet, "not a url", nil).Check(context.Background())
			ExpectThat(t, err).Is(Error(ErrURLCheckFailed))
		}).
		Run("CheckHTTPResponse/invalid_method", func(t *testing.T, f *HTTPServerFixture) {
			err := CheckHTTPResponse("not a method", "not a url", nil).Check(context.Background())
			ExpectThat(t, err).Is(Error(ErrURLCheckFailed))
		})
}

type pinger struct {
	error
}

func (p pinger) PingContext(_ context.Context) error { return p.error }

func TestCheckPing(t *testing.T) {
	t.Run("failure", func(t *testing.T) {
		p := pinger{errors.New("failed")}

		got := CheckPing(p).Check(context.Background())
		ExpectThat(t, got).Is(Error(ErrPingCheckFailed))
	})

	t.Run("success", func(t *testing.T) {
		p := pinger{nil}

		got := CheckPing(p).Check(context.Background())
		ExpectThat(t, got).Is(NoError())
	})
}
