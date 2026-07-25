package api

import (
	"errors"
	"net/http"
	"strings"
)

func (s *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		su, err := s.sessionFromRequest(r)
		if err != nil {
			if errors.Is(err, errNoSession) {
				s.clearSessionCookie(w)
				writeProblem(w, http.StatusUnauthorized, "unauthorized", "authentication required")
				return
			}
			s.writeInternalError(w, "auth middleware: resolve session", err)
			return
		}

		next.ServeHTTP(w, r.WithContext(withSessionUser(r.Context(), su)))
	})
}

func requireRequestedWith(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isStateChanging(r.Method) && strings.TrimSpace(r.Header.Get("X-Requested-With")) == "" {
			writeProblem(w, http.StatusForbidden, "forbidden", "X-Requested-With header is required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isStateChanging(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}
