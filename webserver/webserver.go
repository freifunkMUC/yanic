package webserver

import (
	"net/http"

	"github.com/NYTimes/gziphandler"

	"codeberg.org/FreifunkBremen/yanic/lib/log"
)

// New creates a new webserver and starts it
func New(bindAddr, webroot string) *http.Server {
	mux := http.NewServeMux()
	if webroot != "" {
		mux.Handle("/", gziphandler.GzipHandler(http.FileServer(http.Dir(webroot))))
	}
	return &http.Server{
		Addr:    bindAddr,
		Handler: mux,
	}
}

func Start(srv *http.Server) {
	// service connections
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Panic("webserver crashed", log.AttrError(err))
	}
}
