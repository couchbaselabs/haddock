package server

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"cod/internal/logger"

	"go.uber.org/zap"
)

// handleCouchbaseAPIProxy proxies direct API requests (e.g., XHR from UI) to the appropriate cluster.
// It determines the target cluster based on the Referer header.
func (s *Server) handleCouchbaseAPIProxy(w http.ResponseWriter, r *http.Request) {
	// Determine cluster from referer header
	referer := r.Header.Get("Referer")
	if referer == "" {
		logger.Log.Warn("API request rejected - missing referer header",
			zap.String("remoteAddr", r.RemoteAddr),
			zap.String("path", r.URL.Path))
		http.Error(w, "API requests require a Referer header to determine target cluster", http.StatusBadRequest)
		return
	}

	// Extract cluster name from referer path (/cui/<clustername>/...)
	refererURL, err := url.Parse(referer)
	if err != nil {
		logger.Log.Warn("API request rejected - invalid referer format",
			zap.Error(err),
			zap.String("referer", referer),
			zap.String("remoteAddr", r.RemoteAddr))
		http.Error(w, "Invalid referer URL", http.StatusBadRequest)
		return
	}

	refPath := refererURL.Path
	if !strings.HasPrefix(refPath, "/cui/") {
		logger.Log.Warn("API request rejected - referer path not starting with /cui/",
			zap.String("refererPath", refPath),
			zap.String("remoteAddr", r.RemoteAddr))
		http.Error(w, "Referer path must start with /cui/", http.StatusBadRequest)
		return
	}

	parts := strings.SplitN(strings.TrimPrefix(refPath, "/cui/"), "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		logger.Log.Warn("API request rejected - could not extract cluster name",
			zap.String("refererPath", refPath),
			zap.String("remoteAddr", r.RemoteAddr))
		http.Error(w, "Could not determine cluster name from referer", http.StatusBadRequest)
		return
	}

	clusterName := parts[0]

	// Namespace is checked in server.Start()

	// Use the cluster name directly as the service name instead of appending "-ui"
	svcName := clusterName + "-ui"

	// Construct the target URL (e.g., http://mycluster-ui.mynamespace.svc.cluster.local:8091)
	targetURL := &url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("%s.%s.svc.cluster.local:8091", svcName, s.namespace),
	}

	// Verbose logging for non-GET or settings-related API requests
	if r.Method != "GET" || strings.Contains(r.URL.Path, "/settings") {
		logger.Log.Info("Proxying API request",
			zap.String("cluster", clusterName),
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
			zap.String("targetURL", targetURL.String()))
	}

	proxy := httputil.NewSingleHostReverseProxy(targetURL)

	// Custom error handler for proxy failures
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		logger.Log.Error("API proxy error",
			zap.Error(err),
			zap.String("cluster", clusterName),
			zap.String("namespace", s.namespace),
			zap.String("path", r.URL.Path),
			zap.String("method", r.Method),
			zap.String("remoteAddr", r.RemoteAddr))
		http.Error(w, fmt.Sprintf("API Proxy error: %v", err), http.StatusBadGateway)
	}

	// Update the Host header and target
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		// Set up the request
		originalDirector(req)
		req.Host = targetURL.Host
		req.URL.Scheme = targetURL.Scheme
		req.URL.Host = targetURL.Host
		// Keep the original path
	}

	// Forward the request through the proxy
	proxy.ServeHTTP(w, r)
}

// handleCouchbaseUIProxy handles reverse proxy requests for the Couchbase UI itself.
// It proxies requests like /cui/<clustername>/... to the cluster's UI service,
// rewriting paths and handling redirects.
func (s *Server) handleCouchbaseUIProxy(w http.ResponseWriter, r *http.Request) {
	// Extract cluster name from URL path: /cui/<clustername>/...
	path := r.URL.Path
	parts := strings.SplitN(strings.TrimPrefix(path, "/cui/"), "/", 2)

	if len(parts) == 0 || parts[0] == "" {
		logger.Log.Warn("UI proxy request rejected - missing cluster name",
			zap.String("path", path),
			zap.String("remoteAddr", r.RemoteAddr))
		http.Error(w, "Cluster name is required", http.StatusBadRequest)
		return
	}

	clusterName := parts[0]

	// Namespace is checked in server.Start()

	// Use the cluster name directly as the service name instead of appending "-ui"
	svcName := clusterName + "-ui"

	// Construct the target URL (e.g., http://mycluster-ui.mynamespace.svc.cluster.local:8091)
	targetURL := &url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("%s.%s.svc.cluster.local:8091", svcName, s.namespace),
	}

	// For production logging, only log the initial access to a cluster UI
	// and not every asset/resource request
	if len(parts) <= 1 || parts[1] == "" || parts[1] == "/" {
		logger.Log.Info("Proxying to Couchbase UI homepage",
			zap.String("cluster", clusterName),
			zap.String("remoteAddr", r.RemoteAddr))
	}

	// Create a reverse proxy
	proxy := httputil.NewSingleHostReverseProxy(targetURL)

	// Custom error handler for proxy failures
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		logger.Log.Error("UI proxy error",
			zap.Error(err),
			zap.String("cluster", clusterName),
			zap.String("namespace", s.namespace),
			zap.String("path", r.URL.Path),
			zap.String("remoteAddr", r.RemoteAddr))
		http.Error(w, fmt.Sprintf("Proxy error: %v", err), http.StatusBadGateway)
	}

	// Modify the request before forwarding
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		// Let the default director set up basic proxy fields (e.g., X-Forwarded-For)
		originalDirector(req)

		// Set the Host header
		req.Host = targetURL.Host

		// Point request URL to the target
		req.URL.Scheme = targetURL.Scheme
		req.URL.Host = targetURL.Host

		// Rewrite path: remove /cui/<clustername> prefix
		cuiPrefix := fmt.Sprintf("/cui/%s", clusterName)
		strippedPath := strings.TrimPrefix(req.URL.Path, cuiPrefix)
		req.URL.Path = strippedPath
	}

	// Modify the response after it comes back from the target service
	proxy.ModifyResponse = func(resp *http.Response) error {
		// Rewrite redirect Location headers
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			location := resp.Header.Get("Location")
			if location != "" {
				redirectURL, err := url.Parse(location)
				if err != nil {
					logger.Log.Error("Failed to parse redirect URL",
						zap.Error(err),
						zap.String("location", location),
						zap.String("cluster", clusterName))
				} else {
					// Prepend /cui/<clustername> prefix to the path
					path := redirectURL.Path
					newLocation := fmt.Sprintf("/cui/%s%s", clusterName, path)
					resp.Header.Set("Location", newLocation)

					logger.Log.Debug("Rewrote redirect location",
						zap.String("originalLocation", location),
						zap.String("newLocation", newLocation),
						zap.String("cluster", clusterName))
				}
			}
		}
		// Note: Could potentially modify HTML to inject <base href> if needed.
		return nil
	}

	// Forward the request through the proxy
	proxy.ServeHTTP(w, r)
}
