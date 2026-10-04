package main

import (
	"bytes"
	"log"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

// registeredRoute records one method+pattern registered on the router, so
// tests can walk the full route table (stdlib's ServeMux exposes no public
// walk API, unlike chi.Walk) without re-deriving it from buildRouter's source.
type registeredRoute struct {
	method  string
	pattern string
}

// routeBuilder wraps http.ServeMux registration so buildRouter can both
// dispatch requests and hand the full route table to tests (#319).
type routeBuilder struct {
	mux    *http.ServeMux
	routes []registeredRoute
}

func newRouteBuilder() *routeBuilder {
	return &routeBuilder{mux: http.NewServeMux()}
}

// handle registers pattern for method, wrapping handler with mw (outermost
// first) if non-nil. pattern excludes the method prefix net/http patterns
// normally require ("GET ") — handle adds it.
func (b *routeBuilder) handle(method, pattern string, mw func(http.Handler) http.Handler, handler http.HandlerFunc) {
	b.routes = append(b.routes, registeredRoute{method, pattern})
	var h http.Handler = handler
	if mw != nil {
		h = mw(h)
	}
	b.mux.Handle(method+" "+pattern, h)
}

// chain composes middleware so the first argument runs outermost (first),
// matching the order chi.Router.Use applied them.
func chain(mws ...func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(final http.Handler) http.Handler {
		for i := len(mws) - 1; i >= 0; i-- {
			final = mws[i](final)
		}
		return final
	}
}

// handleWildcard registers prefix+"{rest...}" for method (wrapped in mw),
// plus an exact sibling for the bare prefix (no trailing slash) using
// bareHandler. net/http's ServeMux otherwise silently 307-redirects that
// bare request to prefix+"{rest...}" before any handler runs — any
// "x/{name...}" pattern implies the subtree pattern "x/" the same way a bare
// "x" implies it — so without the sibling, a bare "/local" would 307 to
// "/local/" instead of reaching bareHandler the way chi's old "/local/*" did
// when dialed without the trailing slash (#319). prefix must end in "/".
// Most callers pass h.NotFound as bareHandler, since no meaningful
// bare-prefix page exists; the PO/supplier folder and folder-upload routes
// pass their real bare-prefix handler instead, since that bare path is a
// legitimate route (the folder root), not a dead end (#328). bareHandler
// runs behind the same mw as handler, so an auth-gated prefix redirects an
// unauthenticated bare request to /login rather than reaching bareHandler
// at all (#327).
func (b *routeBuilder) handleWildcard(method, prefix string, mw func(http.Handler) http.Handler, handler, bareHandler http.HandlerFunc) {
	b.handle(method, prefix+"{rest...}", mw, handler)
	b.handle(method, strings.TrimSuffix(prefix, "/"), mw, bareHandler)
}

// withNotFound serves mux normally. A request whose path matches no
// registered pattern gets notFound instead of net/http's plain-text default
// — chi.Mux previously ran its own NotFoundHandler the same way via
// r.NotFound. mux.Handler reports pattern=="" both for that true-404 case
// and for a path that's registered under a different method (ServeMux
// itself already tells them apart internally, returning its own synthesized
// 405-with-Allow-header handler for the latter) — so the handler it returns
// is probed to see which one it is, letting a real 405 pass through
// stdlib's own correct response while a true 404 still gets notFound's page
// instead of net/http's plain-text default (#319).
func withNotFound(mux *http.ServeMux, notFound http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h, pattern := mux.Handler(r)
		if pattern == "" {
			probe := &responseProbe{}
			h.ServeHTTP(probe, r)
			if probe.status == http.StatusNotFound {
				notFound(w, r)
				return
			}
			probe.flush(w)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// responseProbe buffers a response so withNotFound can see the status
// mux's own NotFoundHandler/405 handler would send before committing
// anything to the real ResponseWriter — a true 404 must never reach it,
// since notFound renders a different body in its place.
type responseProbe struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (p *responseProbe) Header() http.Header {
	if p.header == nil {
		p.header = make(http.Header)
	}
	return p.header
}

func (p *responseProbe) WriteHeader(status int) {
	if p.status == 0 {
		p.status = status
	}
}

func (p *responseProbe) Write(b []byte) (int, error) {
	if p.status == 0 {
		p.status = http.StatusOK
	}
	return p.body.Write(b)
}

func (p *responseProbe) flush(w http.ResponseWriter) {
	dst := w.Header()
	for k, v := range p.header {
		dst[k] = v
	}
	status := p.status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	w.Write(p.body.Bytes())
}

// statusRecorder captures the status code a handler writes so loggingMiddleware
// can report it after ServeHTTP returns. Like the real net/http response
// writer, only the first WriteHeader call counts — a handler that (as
// h.NotFound can, via a failed template render) writes headers twice must
// still log the status it actually sent.
type statusRecorder struct {
	http.ResponseWriter
	status    int
	wroteOnce bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wroteOnce {
		s.status = code
		s.wroteOnce = true
	}
	s.ResponseWriter.WriteHeader(code)
}

// loggingMiddleware replaces chi/middleware.Logger with the one line per
// request this app actually used it for.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.Printf("%s %s from %s - %d %s", r.Method, r.URL.RequestURI(), r.RemoteAddr, rec.status, time.Since(start))
	})
}

// recovererMiddleware replaces chi/middleware.Recoverer: it turns a panic in
// any handler into a 500 instead of taking down the whole listener.
func recovererMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rvr := recover(); rvr != nil {
				log.Printf("panic: %v\n%s", rvr, debug.Stack())
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
