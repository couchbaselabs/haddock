package server

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"

	"cod/internal/logger"

	"go.uber.org/zap"
)

// handleCouchbaseAPIProxy handles API requests that need to be forwarded to the active Couchbase cluster
func (s *Server) handleCouchbaseAPIProxy(w http.ResponseWriter, r *http.Request) {
	// Determine which cluster to use by checking the referer header
	referer := r.Header.Get("Referer")
	if referer == "" {
		http.Error(w, "API requests require a Referer header to determine target cluster", http.StatusBadRequest)
		return
	}

	// Extract cluster name from the referer
	// Expected format: http://host:port/cui/clustername/...
	refererURL, err := url.Parse(referer)
	if err != nil {
		http.Error(w, "Invalid referer URL", http.StatusBadRequest)
		return
	}

	refPath := refererURL.Path
	if !strings.HasPrefix(refPath, "/cui/") {
		http.Error(w, "Referer path must start with /cui/", http.StatusBadRequest)
		return
	}

	parts := strings.SplitN(strings.TrimPrefix(refPath, "/cui/"), "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "Could not determine cluster name from referer", http.StatusBadRequest)
		return
	}

	clusterName := parts[0]
	logger.Log.Info("API request to", zap.String("path", r.URL.Path), zap.String("cluster", clusterName), zap.String("referer", referer))

	// Get the namespace from environment variable
	namespace := os.Getenv("WATCH_NAMESPACE")
	if namespace == "" {
		http.Error(w, "WATCH_NAMESPACE environment variable not set", http.StatusInternalServerError)
		return
	}

	// Create the service name for the Couchbase UI (following standard naming)
	svcName := clusterName + "-ui"

	// Create the target URL - using port 8091 which is the Couchbase UI port
	targetURL := &url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("%s.%s.svc.cluster.local:8091", svcName, namespace),
	}

	logger.Log.Info("Proxying API request to", zap.String("targetURL", targetURL.String()))

	// Create a reverse proxy
	proxy := httputil.NewSingleHostReverseProxy(targetURL)

	// Handle errors
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		logger.Log.Error("API Proxy error", zap.Error(err))
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
		logger.Log.Info("Final API request", zap.String("requestURL", req.URL.String()))
	}

	// Serve the proxy request
	proxy.ServeHTTP(w, r)
}

// handleCouchbaseUIProxy handles reverse proxy requests to Couchbase UI
func (s *Server) handleCouchbaseUIProxy(w http.ResponseWriter, r *http.Request) {
	// Extract the cluster name from the URL path
	// URL format: /cui/clustername/...
	path := r.URL.Path
	parts := strings.SplitN(strings.TrimPrefix(path, "/cui/"), "/", 2)

	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "Cluster name is required", http.StatusBadRequest)
		return
	}

	clusterName := parts[0]
	logger.Log.Info("Proxying to Couchbase UI for cluster", zap.String("cluster", clusterName), zap.String("path", path))

	// Get the namespace from environment variable
	namespace := os.Getenv("WATCH_NAMESPACE")
	if namespace == "" {
		http.Error(w, "WATCH_NAMESPACE environment variable not set", http.StatusInternalServerError)
		return
	}

	// Create the service name for the Couchbase UI (following standard naming)
	svcName := clusterName + "-ui"

	// Create the target URL - using port 8091 which is the Couchbase UI port
	targetURL := &url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("%s.%s.svc.cluster.local:8091", svcName, namespace),
	}

	// Create a reverse proxy
	proxy := httputil.NewSingleHostReverseProxy(targetURL)

	// Handle errors
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		logger.Log.Error("Proxy error", zap.Error(err))
		http.Error(w, fmt.Sprintf("Proxy error: %v", err), http.StatusBadGateway)
	}

	// Update the Host header and target
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		// First, let the default director set up the request
		originalDirector(req)

		// Set the Host header
		req.Host = targetURL.Host

		// Create a new URL with the target scheme and host
		req.URL.Scheme = targetURL.Scheme
		req.URL.Host = targetURL.Host

		// Strip /cui/clustername from the path
		cuiPrefix := fmt.Sprintf("/cui/%s", clusterName)
		strippedPath := strings.TrimPrefix(req.URL.Path, cuiPrefix)
		req.URL.Path = strippedPath
	}

	// Modify the response to handle redirects and add base href
	proxy.ModifyResponse = func(resp *http.Response) error {
		// Handle redirects (3xx responses)
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			location := resp.Header.Get("Location")
			if location != "" {
				logger.Log.Info("Original redirect location", zap.String("location", location))
				redirectURL, err := url.Parse(location)
				if err != nil {
					logger.Log.Error("Error parsing redirect URL", zap.Error(err))
				} else {
					// Extract the path and rewrite it to our proxy format
					path := redirectURL.Path
					newLocation := fmt.Sprintf("/cui/%s%s", clusterName, path)
					resp.Header.Set("Location", newLocation)
				}
			}
		}
		return nil
	}

	// Serve the proxy request directly without path rewriting
	proxy.ServeHTTP(w, r)
}
