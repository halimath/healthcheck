package healthcheck

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/halimath/expect-go"
	. "github.com/halimath/fixture"
)

type handlerFixture struct {
	handler *Handler
}

func (h *handlerFixture) BeforeAll(t *testing.T) error {
	h.handler = New(nil)
	return nil
}

func (h *handlerFixture) LivenessRequest() int {
	r := httptest.NewRequest(http.MethodGet, "/livez", nil)
	w := httptest.NewRecorder()

	h.handler.ServeHTTP(w, r)

	return w.Result().StatusCode
}

func (h *handlerFixture) ReadynessRequest() int {
	r := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()

	h.handler.ServeHTTP(w, r)

	return w.Result().StatusCode
}

func TestHandler(t *testing.T) {
	With(t, new(handlerFixture)).
		Run("liveness", func(t *testing.T, f *handlerFixture) {
			got := f.LivenessRequest()
			ExpectThat(t, got).Is(Equal(http.StatusNoContent))
		}).
		Run("readyness_noCheck", func(t *testing.T, f *handlerFixture) {
			got := f.ReadynessRequest()
			ExpectThat(t, got).Is(Equal(http.StatusNoContent))
		}).
		Run("readyness_singleSuccessfulCheck", func(t *testing.T, f *handlerFixture) {
			f.handler.AddCheckFunc(func(context.Context) error { return nil })
			got := f.ReadynessRequest()
			ExpectThat(t, got).Is(Equal(http.StatusNoContent))
		}).
		Run("readyness_failingCheck", func(t *testing.T, f *handlerFixture) {
			f.handler.AddCheckFunc(func(context.Context) error { return errors.New("failed") })
			got := f.ReadynessRequest()
			ExpectThat(t, got).Is(Equal(http.StatusServiceUnavailable))
		})
}
