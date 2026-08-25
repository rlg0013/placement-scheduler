package api

import (
	"net/http"
	"strings"

	"placement-scheduler/pkg/bootstrap"
)

var server = bootstrap.NewServer()

func Handler(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api") {
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/api")
		if r.URL.Path == "" {
			r.URL.Path = "/"
		}
	}

	server.Routes().ServeHTTP(w, r)
}
