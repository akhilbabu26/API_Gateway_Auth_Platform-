package internal

import "net/http"

// CORS returns a middleware enforcing strict Cross-Origin Resource Sharing rules.
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	allowedMap := make(map[string]bool)
	allowAny := false

	for _, o := range allowedOrigins {
		if o == "*" {
			allowAny = true
		} else {
			allowedMap[o] = true
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			isAllowed := false

			if allowAny {
				isAllowed = true
			} else if origin != "" && allowedMap[origin] {
				isAllowed = true
			}

			if isAllowed {
				if origin != "" {
					w.Header().Set("Access-Control-Allow-Origin", origin)
				} else if allowAny {
					w.Header().Set("Access-Control-Allow-Origin", "*")
				}
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Accept, Authorization, Content-Type, X-CSRF-Token, X-Tenant-ID")
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Max-Age", "86400")
			}

			if r.Method == http.MethodOptions {
				if !isAllowed && origin != "" {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
