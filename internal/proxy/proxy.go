package proxy

import (
	"net/http"
)

// interface to define the proxy methods
type Proxy interface {
	// function to handle the request
	Handle(w http.ResponseWriter, r *http.Request)
}