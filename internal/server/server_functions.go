package server

import (
	"context"
	"encoding/json"
	"html/template"
	"io"
	"net/http"
	"strings"
	"time"

	"cod/internal/cluster"
	"cod/internal/events"
	"cod/internal/logger"
	"cod/internal/logs"
	"cod/internal/utils"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/prom2json"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"
)

// isAPIRequest attempts to heuristically determine if an HTTP request is intended for the
// backend Couchbase API rather than a UI navigation request.
func (s *Server) isAPIRequest(r *http.Request) bool {
	// Check for common XHR header
	if r.Header.Get("X-Requested-With") == "XMLHttpRequest" {
		return true
	}

	// Check Accept header: Requests accepting JSON/XML but *not* explicitly HTML are likely API calls.
	accept := r.Header.Get("Accept")
	if strings.Contains(accept, "application/json") ||
		strings.Contains(accept, "application/xml") ||
		strings.Contains(accept, "*/*") {
		// Exclude requests that explicitly want HTML (likely browser navigation)
		if !strings.Contains(accept, "text/html") {
			return true
		}
	}

	// Check for JSON content type header
	if r.Header.Get("Content-Type") == "application/json" {
		return true
	}

	// Explicit browser navigation headers usually indicate it's *not* an API request.
	if strings.Contains(accept, "text/html") &&
		r.Method == "GET" &&
		r.Header.Get("Sec-Fetch-Mode") == "navigate" {
		return false
	}

	// Fallback: Check for common Couchbase API path prefixes.
	apiPrefixes := []string{
		"/pools",
		"/settings",
		"/controller",
		"/nodes",
		"/indexes",
		"/query",
	}

	for _, prefix := range apiPrefixes {
		if strings.HasPrefix(r.URL.Path, prefix) {
			return true
		}
	}

	return false
}

// handleConnections upgrades HTTP requests to WebSocket connections and manages the client lifecycle.
func (s *Server) handleConnections(w http.ResponseWriter, r *http.Request) {
	remoteAddr := r.RemoteAddr
	userAgent := r.UserAgent()

	ws, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		logger.Log.Error("Failed to upgrade to websocket",
			zap.Error(err),
			zap.String("remoteAddr", remoteAddr))
		http.Error(w, "WebSocket upgrade failed", http.StatusInternalServerError)
		return
	}
	defer ws.Close() // Ensure connection is closed when function returns

	// Initialize client state
	client := &Client{
		conn:            ws,
		watchEventslist: make(map[string]bool),
		logWatcher:      nil,
		logSessionId:    "",
		eventSessionId:  "",
		sendingCached:   false,
		eventQueue:      make([]utils.Message, 0),
	}

	// Register client with the server
	s.clientsMapMutex.Lock()
	s.clients[client] = true
	clientCount := len(s.clients)
	s.clientsMapMutex.Unlock()

	logger.Log.Info("Client connected",
		zap.String("remoteAddr", remoteAddr),
		zap.String("userAgent", userAgent),
		zap.Int("activeClients", clientCount))

	// Immediately load and broadcast initial cluster conditions for the new client.
	// Done in a goroutine to avoid blocking the connection setup.
	go func() {
		err := cluster.LoadClusterConditions(s.dynamicClient, s.updateConditions)
		if err != nil {
			logger.Log.Error("Failed to load cluster conditions for new client",
				zap.Error(err),
				zap.String("remoteAddr", remoteAddr))
		}
	}()

	// --- Client Disconnect Cleanup ---
	// This defer runs when the main loop exits (connection closes or error).
	defer func() {
		// Cancel any active log watcher for this client
		client.stateMutex.Lock()
		logWatcher := client.logWatcher
		logSessionId := client.logSessionId
		client.stateMutex.Unlock()

		if logWatcher != nil {
			logWatcher() // Call context cancel func
			logger.Log.Debug("Client log watcher canceled",
				zap.String("sessionId", logSessionId),
				zap.String("remoteAddr", remoteAddr))
		}

		// Unregister client (remove from server maps)
		s.clientsMapMutex.Lock() // Lock 1
		delete(s.clients, client)
		remainingClients := len(s.clients)
		s.clientsMapMutex.Unlock() // Unlock 1

		s.pendingClientsMutex.Lock() // Lock 2
		delete(s.pendingClients, client)
		s.pendingClientsMutex.Unlock() // Unlock 2

		logger.Log.Info("Client disconnected",
			zap.String("remoteAddr", remoteAddr),
			zap.Int("remainingClients", remainingClients))

		// Check if event watchers can be cleaned up now that this client left.
		// Requires computing active clusters *after* client is removed.
		activeClustersForCleanup := s.getActiveClusters()
		s.cleanupEventWatchers(activeClustersForCleanup)
	}()

	// --- Main Client Message Loop ---
	for {
		// Read incoming messages from the client WebSocket
		_, message, err := ws.ReadMessage()
		if err != nil {
			// Distinguish normal closure from errors
			if !strings.Contains(err.Error(), "websocket: close") {
				logger.Log.Info("WebSocket read error (client likely disconnected)",
					zap.Error(err),
					zap.String("remoteAddr", remoteAddr))
			}
			break // Exit loop on error or close
		}

		// Decode the JSON message
		var request struct {
			Type       string          `json:"type"`
			Clusters   []string        `json:"clusters,omitempty"`
			SessionID  string          `json:"sessionId,omitempty"`
			StartTime  string          `json:"startTime,omitempty"`
			EndTime    string          `json:"endTime,omitempty"`
			Follow     bool            `json:"follow,omitempty"`
			ClusterMap map[string]bool `json:"clusterMap,omitempty"`
		}
		if err := json.Unmarshal(message, &request); err != nil {
			logger.Log.Error("Failed to parse client message",
				zap.Error(err),
				zap.String("rawMessage", string(message)),
				zap.String("remoteAddr", remoteAddr))
			continue
		}

		switch request.Type {
		case "clustersevents":
			// Handle Request for Cluster Events

			// Set session ID for future events
			client.stateMutex.Lock()
			client.eventSessionId = request.SessionID
			client.stateMutex.Unlock()

			logger.Log.Debug("Client requested cluster events",
				zap.String("sessionId", request.SessionID),
				zap.Strings("clusters", request.Clusters),
				zap.String("remoteAddr", remoteAddr))

			// Clear any previous event queue
			client.stateMutex.Lock()
			client.eventQueue = nil
			client.stateMutex.Unlock()

			// Ensure client isn't marked as pending
			s.pendingClientsMutex.Lock()
			delete(s.pendingClients, client)
			s.pendingClientsMutex.Unlock()

			// Update watched clusters
			client.watchEventslistMutex.Lock()
			client.watchEventslist = make(map[string]bool)
			for _, cluster := range request.Clusters {
				client.watchEventslist[cluster] = true
				s.startWatcher(cluster) // Start K8s event watcher if needed

				// Send cached events asynchronously
				go s.sendCachedEvents(client, cluster)
			}
			client.watchEventslistMutex.Unlock()

			// Trigger cleanup check
			activeClusters := s.getActiveClusters()
			s.cleanupEventWatchers(activeClusters)

		case "logs":
			// Handle Request for Log Streaming

			// Empty SessionID means stop streaming
			if request.SessionID == "" {
				client.stateMutex.Lock()
				logWatcher := client.logWatcher
				logSessionId := client.logSessionId
				client.logSessionId = ""
				client.logWatcher = nil
				client.stateMutex.Unlock()

				if logWatcher != nil {
					logWatcher() // Call context cancel
					logger.Log.Debug("Log watcher stopped by client request",
						zap.String("sessionId", logSessionId),
						zap.String("remoteAddr", remoteAddr))
				}
				continue
			}

			// Start a new log watcher
			// Cancel any previous watcher for this client
			client.stateMutex.Lock()
			logWatcher := client.logWatcher
			prevLogSessionId := client.logSessionId
			client.logSessionId = request.SessionID
			client.stateMutex.Unlock()

			if logWatcher != nil {
				logWatcher() // Call context cancel
				logger.Log.Debug("Previous log watcher canceled for new request",
					zap.String("prevSessionId", prevLogSessionId),
					zap.String("newSessionId", request.SessionID))
			}

			// Parse optional start/end times
			var startTime *time.Time
			var endTime *time.Time

			if request.StartTime != "" {
				t, err := time.Parse(time.RFC3339, request.StartTime)
				if err != nil {
					logger.Log.Warn("Invalid start time format, ignoring",
						zap.String("startTime", request.StartTime),
						zap.String("sessionId", request.SessionID),
						zap.String("remoteAddr", remoteAddr))
				} else {
					startTime = &t
				}
			}

			// End time only relevant if not following
			if !request.Follow && request.EndTime != "" {
				t, err := time.Parse(time.RFC3339, request.EndTime)
				if err != nil {
					logger.Log.Warn("Invalid end time format, ignoring",
						zap.String("endTime", request.EndTime),
						zap.String("sessionId", request.SessionID),
						zap.String("remoteAddr", remoteAddr))
				} else {
					endTime = &t
				}
			}

			// Start the log watching goroutine
			s.startLogWatcher(client, startTime, endTime, request.Follow, request.ClusterMap, request.SessionID)
		}
	}
}

func (s *Server) startWatcher(clusterName string) {
	s.eventWatchersMutex.Lock()
	defer s.eventWatchersMutex.Unlock()

	if _, exists := s.eventWatchers[clusterName]; exists {
		logger.Log.Debug("Event watcher already exists for cluster",
			zap.String("cluster", clusterName))
		return
	}

	// Load initial events into the cache
	initialEvents := events.GetInitialEvents(s.clientset, s.dynamicClient, clusterName)
	eventCount := len(initialEvents)

	logger.Log.Info("Starting event watcher for cluster",
		zap.String("cluster", clusterName),
		zap.Int("initialEventCount", eventCount))

	s.eventCacheMutex.Lock()
	s.eventCache[clusterName] = initialEvents
	s.eventCacheMutex.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	s.eventWatchers[clusterName] = cancel

	// Start the event watcher (this will send events to the broadcast channel which get added to the cache)
	go events.StartEventWatcher(ctx, s.clientset, s.dynamicClient, clusterName, s.broadcast)
}

// getActiveClusters computes the set of clusters currently being watched by any client.
func (s *Server) getActiveClusters() map[string]bool {
	activeClusters := make(map[string]bool)
	s.clientsMapMutex.RLock()
	for client := range s.clients {
		client.watchEventslistMutex.RLock()
		for cluster := range client.watchEventslist {
			activeClusters[cluster] = true
		}
		client.watchEventslistMutex.RUnlock()
	}
	s.clientsMapMutex.RUnlock()
	return activeClusters
}

// cleanupEventWatchers stops K8s event watchers for clusters that are no longer
// present in the provided activeClusters map.
func (s *Server) cleanupEventWatchers(activeClusters map[string]bool) {
	s.eventWatchersMutex.Lock()
	defer s.eventWatchersMutex.Unlock()

	initialWatcherCount := len(s.eventWatchers)
	removedCount := 0

	for cluster, cancel := range s.eventWatchers {
		// If cluster is no longer watched, stop the watcher
		if !activeClusters[cluster] {
			cancel() // Call context cancel
			delete(s.eventWatchers, cluster)

			// Clean up event cache for the stopped watcher
			s.eventCacheMutex.Lock()
			cachedEventCount := 0
			if events, exists := s.eventCache[cluster]; exists {
				cachedEventCount = len(events)
				delete(s.eventCache, cluster)
			}
			s.eventCacheMutex.Unlock()

			logger.Log.Info("Removed event watcher for inactive cluster",
				zap.String("cluster", cluster),
				zap.Int("cachedEventsRemoved", cachedEventCount))

			removedCount++
		}
	}

	if removedCount > 0 {
		logger.Log.Info("Cleaned up unused event watchers",
			zap.Int("removedWatchers", removedCount),
			zap.Int("initialWatcherCount", initialWatcherCount),
			zap.Int("remainingWatchers", len(s.eventWatchers)))
	} else if initialWatcherCount > 0 {
		logger.Log.Debug("No event watchers to clean up",
			zap.Int("activeWatchers", initialWatcherCount))
	}
}

// startLogWatcher starts a log streaming session for a client.
func (s *Server) startLogWatcher(client *Client, startTime, endTime *time.Time, follow bool, clusterNames map[string]bool, logSessionId string) {
	logContext := []zap.Field{
		zap.String("sessionId", logSessionId),
		zap.Any("clusterNames", clusterNames),
		zap.Bool("follow", follow),
	}

	if startTime != nil {
		logContext = append(logContext, zap.Time("startTime", *startTime))
	}

	if endTime != nil {
		logContext = append(logContext, zap.Time("endTime", *endTime))
	}

	logger.Log.Info("Starting log watcher for client", logContext...)

	ctx, cancel := context.WithCancel(context.Background())
	client.stateMutex.Lock()
	client.logWatcher = cancel
	client.stateMutex.Unlock()
	go logs.StartLogWatcher(ctx, s.clientset, s.broadcast, startTime, endTime, follow, clusterNames, logSessionId)
}

// processPendingClient attempts to send queued events (from client.eventQueue)
// to a single client that was previously marked as busy (sendingCached=true).
// Returns true if the client should be removed from the pending clients map.
func (s *Server) processPendingClient(client *Client) bool {
	client.stateMutex.Lock()
	sendingCached := client.sendingCached
	queueLen := len(client.eventQueue)
	client.stateMutex.Unlock()

	if sendingCached {
		return false // Still sending initial cached events
	}

	if queueLen == 0 {
		return true // No queued events
	}

	logger.Log.Debug("Processing queued events for client", zap.Int("queuedEvents", queueLen))

	// Create a copy of the queue under lock
	client.stateMutex.Lock()
	eventQueueCopy := make([]utils.Message, queueLen)
	copy(eventQueueCopy, client.eventQueue)
	client.eventQueue = nil // Clear the client's queue
	client.stateMutex.Unlock()

	// Double-check client still exists
	s.clientsMapMutex.RLock()
	_, clientExists := s.clients[client]
	s.clientsMapMutex.RUnlock()

	if !clientExists {
		logger.Log.Debug("Client disconnected before queued events could be sent")
		return true // Client gone
	}

	// Try sending all messages from the copied queue
	for _, queuedMsg := range eventQueueCopy {
		if !s.sendToClient(client, queuedMsg) {
			// sendToClient handles cleanup on failure
			return true // Client disconnected during send
		}
	}

	return true // All messages sent successfully
}

// handleMessages is the central message distribution hub.
// Runs in a single goroutine, receiving messages from the broadcast channel
// and distributing them appropriately.
func (s *Server) handleMessages() {
	logger.Log.Info("Starting message handler")

	for {
		// Priority 1: Process Queued Events for Pending Clients
		s.pendingClientsMutex.Lock()
		clientsToProcess := make([]*Client, 0, len(s.pendingClients))
		for client := range s.pendingClients {
			clientsToProcess = append(clientsToProcess, client)
		}
		s.pendingClientsMutex.Unlock()

		clientsToRemoveFromPending := []*Client{}
		for _, client := range clientsToProcess {
			if s.processPendingClient(client) {
				// Mark client for removal from pending map
				clientsToRemoveFromPending = append(clientsToRemoveFromPending, client)
			}
		}

		// Remove clients from the pending map
		if len(clientsToRemoveFromPending) > 0 {
			s.pendingClientsMutex.Lock()
			for _, client := range clientsToRemoveFromPending {
				delete(s.pendingClients, client)
			}
			s.pendingClientsMutex.Unlock()
		}

		// Priority 2: Process Next Message from Broadcast Channel
		msg := <-s.broadcast

		switch msg.Type {
		case "clustersListUpdate":
			// Broadcast latest cluster list
			logger.Log.Debug("Broadcasting clustersListUpdate")
			broadcastPayload := map[string]interface{}{"type": "clusters", "clusters": msg.Clusters}
			s.broadcastToAllClients(broadcastPayload)

		case "conditionsUpdate":
			// Broadcast latest cluster conditions
			logger.Log.Debug("Broadcasting conditionsUpdate")
			broadcastPayload := map[string]interface{}{"type": "clusterConditions", "conditions": msg.Conditions}
			s.broadcastToAllClients(broadcastPayload)

		case "event", "log", "cachedevent": // Route based on message type and client state
			clusterName := msg.ClusterName
			messageType := msg.Type
			logger.Log.Debug("Processing message for specific clients", zap.String("type", messageType), zap.String("cluster", clusterName))

			// Cache new K8s events
			if messageType == "event" {
				s.eventCacheMutex.Lock()
				clusterEvents := s.eventCache[msg.ClusterName]
				if len(clusterEvents) >= 1000 { // Limit cache size
					clusterEvents = clusterEvents[1:]
				}
				cachedMsg := msg
				cachedMsg.Type = "cachedevent" // Store with type "cachedevent"
				s.eventCache[msg.ClusterName] = append(clusterEvents, cachedMsg)
				s.eventCacheMutex.Unlock()
			}

			s.clientsMapMutex.RLock()
			clientCount := len(s.clients)
			var sentCount, failedCount int
			type clientMessagePair struct { // Pair client with its specific message (potentially modified)
				client  *Client
				message utils.Message
			}
			clientsNeedsQueueing := []clientMessagePair{}

			// Determine action for each connected client
			for client := range s.clients {
				sendAction, msgToSendOrQueue := s.determineClientAction(client, msg)

				if sendAction == ActionQueue {
					// Add to list for later queueing
					clientsNeedsQueueing = append(clientsNeedsQueueing, clientMessagePair{client: client, message: msgToSendOrQueue})
				} else if sendAction == ActionSend {
					// Attempt immediate send
					if s.sendToClient(client, msgToSendOrQueue) {
						sentCount++
					} else {
						failedCount++
						// Error/disconnect handled in sendToClient
					}
				}
				// If ActionIgnore, do nothing
			}
			s.clientsMapMutex.RUnlock()

			// Handle queueing outside the RLock to maintain lock order
			if len(clientsNeedsQueueing) > 0 {
				s.clientsMapMutex.RLock()    // Lock 1 (Read)
				s.pendingClientsMutex.Lock() // Lock 2 (Write)

				for _, pair := range clientsNeedsQueueing {
					// Double-check client still exists
					_, clientExists := s.clients[pair.client]
					if !clientExists {
						continue // Skip disconnected client
					}

					// Add client to pending map and message to client's queue
					s.pendingClients[pair.client] = true
					pair.client.stateMutex.Lock()
					pair.client.eventQueue = append(pair.client.eventQueue, pair.message)
					pair.client.stateMutex.Unlock()
				}

				s.pendingClientsMutex.Unlock() // Unlock 2
				s.clientsMapMutex.RUnlock()    // Unlock 1
			}

			// Log summary
			if sentCount > 0 || failedCount > 0 {
				logLevel := logger.Log.Debug
				if failedCount > 0 {
					logLevel = logger.Log.Info // Use Info level if there were failures
				}
				logLevel("Specific message processing summary",
					zap.String("type", messageType),
					zap.String("cluster", clusterName),
					zap.Int("sentTo", sentCount),
					zap.Int("failed", failedCount),
					zap.Int("totalClientsChecked", clientCount))
			}

		default:
			logger.Log.Warn("Received message with unhandled type on broadcast channel", zap.String("type", msg.Type))
		}
	}
}

// Define constants for client actions
type ClientAction int

const (
	ActionIgnore ClientAction = iota
	ActionSend
	ActionQueue
)

// determineClientAction checks if a message should be sent, queued, or ignored for a client.
// Returns the action type and the message (potentially modified with session ID).
func (s *Server) determineClientAction(client *Client, msg utils.Message) (ClientAction, utils.Message) {
	client.watchEventslistMutex.RLock()
	defer client.watchEventslistMutex.RUnlock()
	client.stateMutex.RLock()
	defer client.stateMutex.RUnlock()

	cLogSessionId := client.logSessionId
	cEventSessionId := client.eventSessionId
	sendingCached := client.sendingCached
	watchesCluster := client.watchEventslist[msg.ClusterName]

	msgCopy := msg // Work with a copy

	switch msgCopy.Type {
	case "log":
		if cLogSessionId == msgCopy.SessionID {
			return ActionSend, msgCopy
		}
	case "event":
		if watchesCluster {
			msgCopy.SessionID = cEventSessionId // Set session ID for event
			if sendingCached {
				return ActionQueue, msgCopy // Queue if client is busy with cached events
			}
			return ActionSend, msgCopy // Send immediately
		}
	case "cachedevent":
		if watchesCluster && cEventSessionId == msgCopy.SessionID {
			return ActionSend, msgCopy
		}
	}

	return ActionIgnore, msgCopy // Default to ignore
}

// sendToClient sends a JSON payload to a specific client's WebSocket connection.
// Handles write errors by closing the connection and removing the client from server maps.
// Returns true on success, false on failure.
func (s *Server) sendToClient(client *Client, payload interface{}) bool {
	err := client.conn.WriteJSON(payload)
	if err != nil {
		logger.Log.Warn("Failed to send message to client, disconnecting",
			zap.Error(err),
			zap.String("remoteAddr", client.conn.RemoteAddr().String()))

		client.conn.Close()

		// Remove client from server maps (consistent lock order)
		s.clientsMapMutex.Lock() // Lock 1
		delete(s.clients, client)
		s.clientsMapMutex.Unlock() // Unlock 1

		s.pendingClientsMutex.Lock() // Lock 2
		delete(s.pendingClients, client)
		s.pendingClientsMutex.Unlock() // Unlock 2

		return false // Failure
	}
	return true // Success
}

// broadcastToAllClients sends a payload to all currently connected clients.
// Creates a snapshot of the client list to avoid holding lock during sends.
// Failed sends result in client disconnection and removal.
func (s *Server) broadcastToAllClients(payload interface{}) {
	// Create snapshot under read lock
	s.clientsMapMutex.RLock()
	clientsCopy := make([]*Client, 0, len(s.clients))
	for client := range s.clients {
		clientsCopy = append(clientsCopy, client)
	}
	s.clientsMapMutex.RUnlock()

	failedCount := 0
	clientCount := len(clientsCopy)

	// Iterate over snapshot and send
	for _, client := range clientsCopy {
		err := client.conn.WriteJSON(payload)
		if err != nil {
			failedCount++
			logger.Log.Warn("Failed to send broadcast payload to client, disconnecting",
				zap.Error(err),
				zap.String("remoteAddr", client.conn.RemoteAddr().String()))

			client.conn.Close()

			// Remove client from server maps (consistent lock order)
			s.clientsMapMutex.Lock() // Lock 1
			delete(s.clients, client)
			s.clientsMapMutex.Unlock() // Unlock 1

			s.pendingClientsMutex.Lock() // Lock 2
			delete(s.pendingClients, client)
			s.pendingClientsMutex.Unlock() // Unlock 2
		}
	}

	if failedCount > 0 {
		logger.Log.Info("Broadcast to all clients completed with errors",
			zap.Int("totalClientsAttempted", clientCount),
			zap.Int("failedClients", failedCount))
	}
}

// addCluster handles the addition event from the cluster informer.
// Adds the cluster name to the tracked set if valid.
func (s *Server) addCluster(obj interface{}) {
	logger.Log.Debug("addCluster called with object")

	if obj == nil {
		logger.Log.Warn("Received nil object in addCluster")
		return
	}

	unstructuredObj, ok := obj.(*unstructured.Unstructured)
	if !ok {
		logger.Log.Warn("Object is not an Unstructured type in addCluster")
		return
	}

	clusterName := unstructuredObj.GetName()
	logger.Log.Debug("Processing cluster addition", zap.String("cluster", clusterName))

	// Ignore clusters already being deleted
	if unstructuredObj.GetDeletionTimestamp() != nil {
		logger.Log.Debug("Skipping add for cluster with deletion timestamp", zap.String("cluster", clusterName))
		return
	}

	s.clustersMutex.Lock()
	_, exists := s.clusters[clusterName]

	if !exists {
		s.clusters[clusterName] = struct{}{}
		logger.Log.Info("Added new cluster to map", zap.String("cluster", clusterName))
		s.clustersMutex.Unlock() // Unlock before broadcasting
		s.broadcastClusters()    // Notify clients
	} else {
		logger.Log.Debug("Cluster already in map", zap.String("cluster", clusterName))
		s.clustersMutex.Unlock()
	}
}

// deleteCluster handles the deletion event from the cluster informer.
// Removes the cluster from the tracked set and its conditions cache.
// Handles direct deletes and tombstones.
func (s *Server) deleteCluster(obj interface{}) {
	logger.Log.Debug("deleteCluster called with object")

	if obj == nil {
		logger.Log.Warn("Received nil object in deleteCluster")
		return
	}

	var clusterName string

	// Handle DeletedFinalStateUnknown (e.g., missed delete event due to watcher restart)
	if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		logger.Log.Debug("Received tombstone object in deleteCluster")
		if unstructuredObj, ok := tombstone.Obj.(*unstructured.Unstructured); ok {
			clusterName = unstructuredObj.GetName()
			logger.Log.Info("Extracted cluster name from tombstone", zap.String("cluster", clusterName))
		} else {
			logger.Log.Warn("Tombstone contains non-Unstructured object type", zap.Any("objectType", tombstone.Obj))
			return
		}
	} else if unstructuredObj, ok := obj.(*unstructured.Unstructured); ok {
		clusterName = unstructuredObj.GetName()
		logger.Log.Info("Processing cluster deletion", zap.String("cluster", clusterName))
	} else {
		logger.Log.Warn("Received unknown object type in deleteCluster", zap.Any("objectType", obj))
		return
	}

	if clusterName == "" {
		logger.Log.Warn("Could not determine cluster name for deletion")
		return
	}

	var deleted bool
	s.clustersMutex.Lock() // Lock 1
	_, exists := s.clusters[clusterName]
	if exists {
		delete(s.clusters, clusterName)
		logger.Log.Info("Removed cluster from map", zap.String("cluster", clusterName))
		deleted = true

		// Remove from conditions map
		s.clusterConditionsMutex.Lock() // Lock 2
		if _, condExists := s.clusterConditions[clusterName]; condExists {
			delete(s.clusterConditions, clusterName)
			logger.Log.Info("Removed cluster conditions for", zap.String("cluster", clusterName))
		}
		s.clusterConditionsMutex.Unlock() // Unlock 2
	}
	s.clustersMutex.Unlock() // Unlock 1

	if deleted { // Broadcast only if deleted
		s.broadcastClusters()
		s.broadcastConditions()
	}
}

// broadcastClusters sends the current list of cluster names via the broadcast channel.
func (s *Server) broadcastClusters() {
	// Create snapshot under read lock
	s.clustersMutex.RLock()
	clustersToSend := make([]string, 0, len(s.clusters))
	for clusterName := range s.clusters {
		clustersToSend = append(clustersToSend, clusterName)
	}
	s.clustersMutex.RUnlock()

	s.broadcast <- utils.Message{
		Type:     "clustersListUpdate",
		Clusters: clustersToSend,
	}
	logger.Log.Debug("Sent clustersListUpdate to broadcast channel", zap.Int("clusterCount", len(clustersToSend)))
}

// updateConditions handles update events from the cluster informer,
// caching the `.status.conditions` field.
func (s *Server) updateConditions(obj interface{}) {
	logger.Log.Debug("updateConditions called with object")

	if obj == nil {
		return
	}

	unstructuredObj, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return
	}

	clusterName := unstructuredObj.GetName()
	logger.Log.Debug("Processing conditions for cluster", zap.String("cluster", clusterName))

	// Check if the cluster is still considered active before proceeding
	s.clustersMutex.RLock()
	_, clusterStillActive := s.clusters[clusterName]
	s.clustersMutex.RUnlock()

	if !clusterStillActive {
		logger.Log.Debug("Skipping conditions update for cluster not in active list (likely deleted)", zap.String("cluster", clusterName))
		return
	}

	// Extract conditions from status
	status, found, err := unstructured.NestedMap(unstructuredObj.Object, "status")
	if err != nil {
		logger.Log.Error("Error getting status field", zap.Error(err), zap.String("cluster", clusterName))
		return
	}
	if !found {
		logger.Log.Debug("No status field found in object", zap.String("cluster", clusterName))
		return
	}

	conditionsRaw, found, err := unstructured.NestedSlice(status, "conditions")
	if err != nil {
		logger.Log.Error("Error getting conditions field", zap.Error(err), zap.String("cluster", clusterName))
		return
	}
	if !found || len(conditionsRaw) == 0 {
		logger.Log.Debug("No conditions found in status", zap.String("cluster", clusterName))
		// Still update cache with empty slice below
	}

	// Convert []interface{} to []map[string]interface{}
	conditionsList := make([]map[string]interface{}, 0, len(conditionsRaw))
	for _, condition := range conditionsRaw {
		if condMap, ok := condition.(map[string]interface{}); ok {
			conditionsList = append(conditionsList, condMap)
		} else {
			logger.Log.Warn("Condition item is not a map[string]interface{}", zap.Any("condition", condition))
		}
	}

	// Update cache
	s.clusterConditionsMutex.Lock()
	s.clusterConditions[clusterName] = conditionsList
	s.clusterConditionsMutex.Unlock()
	logger.Log.Debug("Updated conditions cache for cluster", zap.String("cluster", clusterName))

	// Broadcast updated conditions
	s.broadcastConditions()
}

// broadcastConditions sends the complete conditions cache via the broadcast channel.
func (s *Server) broadcastConditions() {
	// Create deep copy under read lock
	s.clusterConditionsMutex.RLock()
	conditionsToSend := make(map[string][]map[string]interface{}) // Outer map copy
	for k, v := range s.clusterConditions {
		// Copy inner slice
		innerCopy := make([]map[string]interface{}, len(v))
		copy(innerCopy, v) // Note: map references inside slice elements are shared
		conditionsToSend[k] = innerCopy
	}
	s.clusterConditionsMutex.RUnlock()

	s.broadcast <- utils.Message{
		Type:       "conditionsUpdate",
		Conditions: conditionsToSend,
	}
	logger.Log.Debug("Sent conditionsUpdate to broadcast channel", zap.Int("clusterCount", len(conditionsToSend)))
}

// sendCachedEvents retrieves cached K8s events for a cluster and sends them
// to a specific client, respecting the client's eventSessionId.
// Sets the client's sendingCached flag while active.
func (s *Server) sendCachedEvents(client *Client, clusterName string) {
	s.eventCacheMutex.RLock()
	events, exists := s.eventCache[clusterName]
	eventCount := len(events)
	s.eventCacheMutex.RUnlock()

	// Get client's current event session ID
	client.stateMutex.RLock()
	cEventSessionId := client.eventSessionId
	client.stateMutex.RUnlock()

	if !exists || eventCount == 0 {
		logger.Log.Debug("No cached events to send",
			zap.String("cluster", clusterName),
			zap.String("sessionId", cEventSessionId))
		return
	}

	logger.Log.Debug("Sending cached events",
		zap.String("cluster", clusterName),
		zap.String("sessionId", cEventSessionId),
		zap.Int("eventCount", eventCount))

	// Set flag indicating cached events are being sent
	client.stateMutex.Lock()
	client.sendingCached = true
	client.stateMutex.Unlock()

	// Use defer to ensure flag is reset
	defer func() {
		client.stateMutex.Lock()
		client.sendingCached = false
		client.stateMutex.Unlock()

		logger.Log.Debug("Completed sending cached events, resetting flag",
			zap.String("cluster", clusterName),
			zap.String("sessionId", cEventSessionId))
	}()

	// Send cached events via the main broadcast channel
	for _, event := range events {
		event.SessionID = cEventSessionId // Ensure correct session ID
		s.broadcast <- event
	}
}

// handleRootRoute serves the main dashboard page at `/`.
func (s *Server) handleRootRoute(w http.ResponseWriter, r *http.Request) {
	// Proxy API requests instead of serving HTML
	if s.isAPIRequest(r) {
		s.handleCouchbaseAPIProxy(w, r)
		return
	}

	// Serve dashboard template
	s.clustersMutex.RLock()
	clustersCopy := make([]string, 0, len(s.clusters))
	for clusterName := range s.clusters {
		clustersCopy = append(clustersCopy, clusterName)
	}
	s.clustersMutex.RUnlock()

	tmpl, err := template.ParseFiles("templates/index.html")
	if err != nil {
		logger.Log.Error("Error parsing index template", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	tmpl.Execute(w, clustersCopy)
}

// handleClusterRoute serves the cluster-specific view page at `/cluster/<clustername>`.
func (s *Server) handleClusterRoute(w http.ResponseWriter, r *http.Request) {
	// Extract cluster name from path
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 3 || pathParts[2] == "" {
		logger.Log.Warn("Invalid cluster route path", zap.String("path", r.URL.Path))
		http.NotFound(w, r)
		return
	}
	clusterName := pathParts[2]

	// Validate cluster exists
	s.clustersMutex.RLock()
	_, clusterExists := s.clusters[clusterName]
	s.clustersMutex.RUnlock()

	if !clusterExists {
		logger.Log.Warn("Requested cluster not found", zap.String("cluster", clusterName))
		http.NotFound(w, r)
		return
	}

	// Render template
	tmpl, err := template.ParseFiles("templates/cluster.html")
	if err != nil {
		logger.Log.Error("Error parsing cluster template", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	data := struct{ Name string }{Name: clusterName}
	if err := tmpl.Execute(w, data); err != nil {
		logger.Log.Error("Error executing cluster template", zap.Error(err), zap.String("cluster", clusterName))
		// Cannot write http.Error as template might have partially written response
	}
}

// handleMetricsEndpoint proxies requests to the operator's Prometheus metrics endpoint (:8383/metrics).
// Filters metrics and returns JSON or text format.
func (s *Server) handleMetricsEndpoint(w http.ResponseWriter, r *http.Request) {
	// Fetch metrics from operator endpoint
	resp, err := http.Get("http://localhost:8383/metrics")
	if err != nil {
		logger.Log.Error("Failed to get metrics from local endpoint", zap.Error(err))
		http.Error(w, "Failed to fetch metrics: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body) // Read body for logging
		logger.Log.Error("Received non-OK status code from metrics endpoint",
			zap.Int("statusCode", resp.StatusCode),
			zap.String("status", resp.Status),
			zap.ByteString("body", bodyBytes))
		http.Error(w, "Failed to fetch metrics: received status "+resp.Status, resp.StatusCode)
		return
	}

	// Handle JSON requests: parse, filter, convert
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Content-Type", "application/json")

		mfChan := make(chan *dto.MetricFamily, 1024)

		err := prom2json.ParseReader(resp.Body, mfChan)
		if err != nil {
			logger.Log.Error("Failed to parse Prometheus metrics format", zap.Error(err))
			http.Error(w, "Failed to parse metrics: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Filter and convert
		var result []*prom2json.Family
		for mf := range mfChan {
			if s.allowedMetrics[mf.GetName()] {
				result = append(result, prom2json.NewFamily(mf))
			}
		}

		// Marshal to JSON
		jsonData, err := json.Marshal(result)
		if err != nil {
			logger.Log.Error("Failed to marshal filtered metrics to JSON", zap.Error(err))
			http.Error(w, "Failed to convert metrics to JSON: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Write(jsonData)
		return
	}

	// Handle non-JSON requests (return raw Prometheus format)
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Log.Error("Failed to read metrics response body", zap.Error(err))
		http.Error(w, "Failed to read metrics: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Write(body)
}
