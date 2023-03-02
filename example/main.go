package main

import (
	"context"
	"log"
	"net/http"

	"github.com/halimath/healthcheck"
)

func main() {
	h := healthcheck.New()

	h.AddCheck(healthcheck.CheckURL("http://localhost:1234/"))
	h.AddCheckFunc(func(ctx context.Context) error {
		// Add your check code here
		return nil
	})

	http.Handle("/health/", http.StripPrefix("/health", h))

	log.Fatal(http.ListenAndServe(":8080", nil))
}
