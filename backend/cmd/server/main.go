package main

import (
	"log"
	"net/http"

	"placement-scheduler/pkg/bootstrap"
)

func main() {
	server := bootstrap.NewServer()

	log.Printf("baseline schedule: %d interview records", len(server.State.Interviews))
	log.Println("listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", server.Routes()))
}
