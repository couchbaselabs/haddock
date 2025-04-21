// ==================== IMPORTS ======================
import { socket, LOG_BATCH_INTERVAL, EVENT_BATCH_INTERVAL, SEARCH_DEBOUNCE_DELAY, generateSessionId, highlightMatches } from './core.js';
import { renderClusterTiles } from './dashboard.js';

// ==================== GLOBALS ======================
let currentLogSessionId = null;
let currentEventSessionId = null;
let logFragment = null;
let batchTimeoutId = null;
let eventFragments = {}; // Map to store event fragments by cluster name
let eventBatchTimeoutId = null;

// Search data storage
let eventsFuse = null;
let logsFuse = null;

// Search functionality
let eventsSearchTimeout = null;
let logsSearchTimeout = null;

// ==================== DOCUMENT READY EVENT =======================
document.addEventListener('DOMContentLoaded', () => {
    initializeControls();
    initializeSearchFunctionality();
    
    // Set up WebSocket event handler
    socket.onmessage = handleWebSocketMessage;
    
    // Initialize page-specific logic
    const hash = window.location.hash.substring(1);
    const initialPage = hash || 'dashboard';
    if (initialPage === 'events') {
        initializeEventsPage();
    } else if (initialPage === 'logs') {
        initializeLogsPage();
    }
});

// ==================== INITIALIZATION FUNCTIONS ==================
function initializeControls() {
    const logsCheckbox = document.getElementById('logsCheckbox'); 
    const clusterCheckboxes = document.querySelectorAll('.cluster-checkbox');
    const followCheckbox = document.getElementById('followCheckbox');
    const autoScrollCheckbox = document.getElementById('autoScrollCheckbox');
    const startTimeInput = document.getElementById('startTime');
    const endTimeInput = document.getElementById('endTime');
    const logsContainerData = document.getElementById('logsContainerData');

    // Initialize time inputs
    startTimeInput.value = '';
    endTimeInput.value = '';
    endTimeInput.disabled = followCheckbox.checked;
   
    // Set up event listeners for logs container scrolling
    logsContainerData.addEventListener('scroll', function() {
        if (autoScrollCheckbox.checked) {
            const isScrolledToBottom = logsContainerData.scrollHeight - logsContainerData.clientHeight <= logsContainerData.scrollTop + 50;
            if (!isScrolledToBottom) {
                autoScrollCheckbox.checked = false;
            }
        }
    });

    // Auto-scroll checkbox event listener
    autoScrollCheckbox.addEventListener('change', function() {
        if (this.checked) {
            logsContainerData.scrollTop = logsContainerData.scrollHeight;
        }
    });
   
    // Cluster selection event listeners
    clusterCheckboxes.forEach(checkbox => {
        checkbox.addEventListener('change', handleClusterSelection);
    });

    // Logs checkbox event listener
    logsCheckbox.addEventListener('change', handleLogsSelection);

    // Follow checkbox event listener
    followCheckbox.addEventListener('change', (event) => {
        endTimeInput.disabled = event.target.checked;
        if (event.target.checked) {
            endTimeInput.value = '';
        }
    });
}

function initializeSearchFunctionality() {
    const eventsSearch = document.getElementById('eventsSearch');
    const logsSearch = document.getElementById('logsSearch');
    const clearEventsSearch = document.getElementById('clearEventsSearch');
    const clearLogsSearch = document.getElementById('clearLogsSearch');
    const eventsContainerData = document.getElementById('eventsContainerData');
    const logsContainerData = document.getElementById('logsContainerData');
    const eventsSearchResults = document.getElementById('eventsSearchResults');
    const logsSearchResults = document.getElementById('logsSearchResults');
    
    // Initialize Fuse.js instances
    initializeFuse();
    
    // Add event listeners for events search with debouncing
    eventsSearch.addEventListener('keydown', () => {
        const query = eventsSearch.value.trim();
        
        // Clear any previous timeout
        if (eventsSearchTimeout) {
            clearTimeout(eventsSearchTimeout);
            eventsSearchTimeout = null;
        }
        
        if (query) {
            // Show the search results container with loading indicator
            eventsContainerData.classList.add('hidden');
            eventsSearchResults.classList.add('active');
            eventsSearchResults.innerHTML = '<div class="search-loading">Searching...</div>';
            
            // Set a new timeout
            eventsSearchTimeout = setTimeout(() => {
                searchEvents(query);
            }, SEARCH_DEBOUNCE_DELAY);
        } else {
            eventsContainerData.classList.remove('hidden');
            eventsSearchResults.classList.remove('active');
        }
    });
    
    // Add event listeners for logs search with debouncing
    logsSearch.addEventListener('keydown', () => {
        const query = logsSearch.value.trim();
        
        // Clear any previous timeout
        if (logsSearchTimeout) {
            clearTimeout(logsSearchTimeout);
            logsSearchTimeout = null;
        }
        
        if (query) {
            // Show the search results container with loading indicator
            logsContainerData.classList.add('hidden');
            logsSearchResults.classList.add('active');
            logsSearchResults.innerHTML = '<div class="search-loading">Searching...</div>';
            
            // Set a new timeout
            logsSearchTimeout = setTimeout(() => {
                searchLogs(query);
            }, SEARCH_DEBOUNCE_DELAY);
        } else {
            logsContainerData.classList.remove('hidden');
            logsSearchResults.classList.remove('active');
        }
    });
    
    // Clear search event listeners
    clearEventsSearch.addEventListener('click', () => {
        eventsSearch.value = '';
        eventsContainerData.classList.remove('hidden');
        eventsSearchResults.classList.remove('active');
    });
    
    clearLogsSearch.addEventListener('click', () => {
        logsSearch.value = '';
        logsContainerData.classList.remove('hidden');
        logsSearchResults.classList.remove('active');
    });
}

function initializeFuse() {
    const eventsOptions = {
        threshold: 0.2,
        ignoreLocation: true,
        includeMatches: true,
        minMatchCharLength: 3,
        keys: ['clusterName', 'kind', 'objectName', 'message']
    };

    const logsOptions = {
        threshold: 0.2,
        ignoreLocation: true,
        includeMatches: true,
        minMatchCharLength: 3,
    };

    // Initialize with empty collections
    eventsFuse = new Fuse([], eventsOptions);
    logsFuse = new Fuse([], logsOptions);
}

// ==================== EVENT HANDLERS ====================


function handleLogsSelection(event) {
    const logsContainerData = document.getElementById('logsContainerData');

    if (event.target.checked) {
        // Validate inputs
        const followCheckbox = document.getElementById('followCheckbox');
        const startTimeInput = document.getElementById('startTime');
        const endTimeInput = document.getElementById('endTime');

        if (!followCheckbox.checked && !startTimeInput.value) {
            alert("Please select a start time when not following logs");
            event.target.checked = false;
            return;
        }

        currentLogSessionId = generateSessionId();

        // Convert start/end times to RFC3339 if present
        let rfcStart = "";
        let rfcEnd = "";

        if (startTimeInput.value) {
            const startDate = new Date(startTimeInput.value);
            rfcStart = startDate.toISOString();
        }
        if (!followCheckbox.checked && endTimeInput.value) {
            const endDate = new Date(endTimeInput.value);
            rfcEnd = endDate.toISOString(); 
        }

        // Get selected clusters and build the clusterMap
        const selectedClusters = Array.from(document.querySelectorAll('.logs-cluster-checkbox:checked')).map(cb => cb.value);
        const clusterMap = {};
        
        // Add each selected cluster to the map
        selectedClusters.forEach(cluster => {
            clusterMap[cluster] = true;
        });

        // Prepare the request
        const request = {
            type: "logs",
            sessionId: currentLogSessionId,
            follow: followCheckbox.checked,
            startTime: rfcStart,
            endTime: rfcEnd,
            clusterMap: clusterMap
        };

        socket.send(JSON.stringify(request));
    } else {
        currentLogSessionId = null;
        // Reset Fuse collection
        if (logsFuse) logsFuse = new Fuse([], logsFuse.options);
        //clear log timeout
        if (batchTimeoutId) {
            clearTimeout(batchTimeoutId);
            batchTimeoutId = null;
        }
        logsContainerData.innerHTML = ''; // Clear logs container visually
        logFragment = null;
        socket.send(JSON.stringify({
            type: "logs",
            sessionId: null
        }));
    }
}

function handleClusterSelection() {
    const selectedClusters = Array.from(document.querySelectorAll('.cluster-checkbox:checked')).map(cb => cb.value);
    
    // Generate a new session ID if we have selected clusters
    if (selectedClusters.length > 0) {
        currentEventSessionId = generateSessionId();
        

    } else {
        currentEventSessionId = null;
    }
    
    // Remove event divs for unselected clusters
    const eventsContainerData = document.getElementById("eventsContainerData");
    const clusterDivs = eventsContainerData.getElementsByClassName('cluster-events');


    //clear event timeout 
    if (eventBatchTimeoutId) {
        clearTimeout(eventBatchTimeoutId);
        eventBatchTimeoutId = null;
    }

    //clear event container and fragments
    eventsContainerData.innerHTML = '';
    eventFragments = {};
    
    // Array.from(clusterDivs).forEach(div => {
    //     const clusterName = div.id.replace('events-', '');
    //     if (!selectedClusters.includes(clusterName)) {
    //         div.remove();
    //         // Remove fragment for this cluster
    //         delete eventFragments[clusterName];
    //     }
    // });

    socket.send(JSON.stringify({
        type: "clustersevents",
        clusters: selectedClusters,
        sessionId: currentEventSessionId
    }));
}

function handleWebSocketMessage(event) {
    const data = JSON.parse(event.data);
    
    if (data.type === "clusters") {
        updateClusters(data.clusters);
        return;
    }
    
    if ((data.type === "event" || data.type === "cachedevent") && data.sessionId === currentEventSessionId) {
        updateEvents(data);
        return;
    }
    
    if (data.type === "log" && data.sessionId === currentLogSessionId) {
        updateLogs(data);
        return;
    }
    
    if (data.type === "clusterConditions") {
        console.log("conditions triggered")
        renderClusterTiles(data.conditions);
        return;
    }
}

// ==================== SEARCH FUNCTIONS ====================
function searchEvents(query) {
    const results = eventsFuse.search(query);
    const eventsSearchResults = document.getElementById('eventsSearchResults');
    eventsSearchResults.innerHTML = '';
    
    if (results.length === 0) {
        eventsSearchResults.innerHTML = '<div class="no-results">No matching events found</div>';
        return;
    }
    
    const fragment = document.createDocumentFragment();
    results.forEach(result => {
        const event = result.item;
        const resultDiv = document.createElement('div');
        resultDiv.className = 'event-entry';
        
        // Process matches for each field
        let clusterHighlighted = event.clusterName || '';
        let kindHighlighted = event.kind || '';
        let nameHighlighted = event.objectName || '';
        let messageHighlighted = event.message || '';
        
        if (result.matches && result.matches.length > 0) {
            // Process each match by field
            result.matches.forEach(match => {
                if (match.key === 'clusterName' && match.indices.length > 0) {
                    clusterHighlighted = highlightMatches(clusterHighlighted, match.indices);
                }
                else if (match.key === 'kind' && match.indices.length > 0) {
                    kindHighlighted = highlightMatches(kindHighlighted, match.indices);
                }
                else if (match.key === 'objectName' && match.indices.length > 0) {
                    nameHighlighted = highlightMatches(nameHighlighted, match.indices);
                }
                else if (match.key === 'message' && match.indices.length > 0) {
                    messageHighlighted = highlightMatches(messageHighlighted, match.indices);
                }
            });
        }
        
        resultDiv.innerHTML = `
            <div class="search-result-cluster">Cluster: ${clusterHighlighted}</div>
            <div class="event-header">
                <span class="event-kind">${kindHighlighted}</span>
                <span class="event-object-name">${nameHighlighted}</span>
            </div>
            <div class="event-message">${messageHighlighted}</div>
        `;
        
        fragment.appendChild(resultDiv);
    });
    
    eventsSearchResults.appendChild(fragment);
    eventsSearchResults.scrollTop = 0;
}

function searchLogs(query) {
    const results = logsFuse.search(query);
    const logsSearchResults = document.getElementById('logsSearchResults');
    logsSearchResults.innerHTML = '';
    
    if (results.length === 0) {
        logsSearchResults.innerHTML = '<div class="no-results">No matching logs found</div>';
        return;
    }

    const fragment = document.createDocumentFragment();
    
    results.forEach(result => {
        const logMessage = result.item; // This is now directly the string
        const resultDiv = document.createElement('div');
        resultDiv.className = 'search-result-item search-result-log';
        
        // Extract matches - they're now directly on the string, not a field
        let highlightedText = logMessage;
        if (result.matches && result.matches.length > 0) {
            // For string items, the matches apply directly to the item
            const match = result.matches[0]; // Should only be one match object
            if (match && match.indices.length > 0) {
                highlightedText = highlightMatches(logMessage, match.indices);
            }
        }
        
        // Use innerHTML to render the highlights
        resultDiv.innerHTML = highlightedText;
        
        fragment.appendChild(resultDiv);
    });
    logsSearchResults.appendChild(fragment);
    logsSearchResults.scrollTop = 0;
}

// ==================== DATA UPDATE FUNCTIONS ====================
function updateClusters(clusters) {
    // Update events page clusters
    updateClusterContainer("clustersContainer", "cluster-checkbox", clusters, handleClusterSelection);
    
    // Update logs page clusters
    updateClusterContainer("logsClusterContainer", "logs-cluster-checkbox", clusters);
}

// Helper function to update a cluster container if there is no change handler do not apply event listener
function updateClusterContainer(containerId, checkboxClass, clusters, changeHandler) {
    const container = document.getElementById(containerId);
    if (!container) return;

    // Add new clusters
    clusters.forEach(cluster => {
        const elementId = containerId === "clustersContainer" ? 
            `cluster-${cluster}` : `logs-cluster-${cluster}`;
            
        if (!document.getElementById(elementId)) {
            const label = document.createElement("label");
            label.id = elementId;
            label.className = containerId === "clustersContainer" ? 
                "cluster-label" : "logs-cluster-label";
                
            label.innerHTML = `
                <input type="checkbox" class="${checkboxClass}" value="${cluster}">
                <span class="${containerId === "clustersContainer" ? 'cluster-name' : 'logs-cluster-name'}">${cluster}</span>
            `;
            container.appendChild(label);
            
            // Add event listener to new checkbox
            const checkbox = label.querySelector(`.${checkboxClass}`);
            if (changeHandler) {
                checkbox.addEventListener('change', changeHandler);
            }
        }
    });

    // Remove old clusters
    const existingLabels = container.getElementsByClassName(
        containerId === "clustersContainer" ? "cluster-label" : "logs-cluster-label"
    );
    
    Array.from(existingLabels).forEach(label => {
        const checkbox = label.querySelector(`.${checkboxClass}`);
        if (checkbox && !clusters.includes(checkbox.value)) {
            label.remove();
        }
    });
}

function updateEvents(eventData) {
    // Add the event data directly to Fuse index
    if (eventsFuse) {
        eventsFuse.add(eventData);
    }
    
    // Check if search is active and update search results
    const eventsSearch = document.getElementById('eventsSearch');
    if (eventsSearch && eventsSearch.value.trim() && !eventsSearchTimeout) {
        searchEvents(eventsSearch.value.trim());
    }
    
    // Create or update cluster div
    let clusterDiv = document.getElementById(`events-${eventData.clusterName}`);
    if (!clusterDiv) {
        clusterDiv = document.createElement("div");
        clusterDiv.id = `events-${eventData.clusterName}`;
        clusterDiv.className = 'cluster-events';
        clusterDiv.innerHTML = `
            <div class="cluster-header">
                <div class="cluster-title">
                    <svg class="collapse-icon" width="12" height="12" viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg">
                        <path d="M7 15L12 10L17 15" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
                    </svg>
                    <h2>${eventData.clusterName}</h2>
                </div>
                <label class="auto-scroll-control">
                    <input type="checkbox" class="events-auto-scroll" checked>
                    <span>Auto scroll</span>
                </label>
            </div>
            <div class="events-content"></div>
        `;
        document.getElementById("eventsContainerData").appendChild(clusterDiv);
        
        // Add scroll event listener to the new events-content
        const eventsContent = clusterDiv.querySelector('.events-content');
        const autoScrollCheckbox = clusterDiv.querySelector('.events-auto-scroll');
        
        eventsContent.addEventListener('scroll', function() {
            if (autoScrollCheckbox.checked) {
                const isScrolledToBottom = eventsContent.scrollHeight - eventsContent.clientHeight <= eventsContent.scrollTop + 50;
                if (!isScrolledToBottom) {
                    autoScrollCheckbox.checked = false;
                }
            }
        });
        
        autoScrollCheckbox.addEventListener('change', function() {
            if (this.checked) {
                eventsContent.scrollTop = eventsContent.scrollHeight;
            }
        });
        
        // Add collapse/expand functionality
        const clusterTitle = clusterDiv.querySelector('.cluster-title');
        clusterTitle.addEventListener('click', function() {
            clusterDiv.classList.toggle('collapsed');
        });
        
        // Initialize fragment for this cluster if it doesn't exist
        if (!eventFragments[eventData.clusterName]) {
            eventFragments[eventData.clusterName] = document.createDocumentFragment();
        }
    }

    // Create the event entry
    const eventEntry = document.createElement('div');
    eventEntry.className = 'event-entry';
    eventEntry.innerHTML = `
        <div class="event-header">
            <span class="event-kind">${eventData.kind}</span>
            <span class="event-object-name">${eventData.objectName}</span>
        </div>
        <div class="event-message">${eventData.message}</div>
    `;
    
    // Add to the fragment for this cluster
    eventFragments[eventData.clusterName].appendChild(eventEntry);
    
    // If we don't have a timer running yet, start one
    if (!eventBatchTimeoutId) {
        eventBatchTimeoutId = setTimeout(flushEventBatches, EVENT_BATCH_INTERVAL);
    }
}

function updateLogs(logData) {
    // Add the log data directly to Fuse index
    if (logsFuse) {
        logsFuse.add(logData.message);
    }
    
    // Check if search is active and update search results
    const logsSearch = document.getElementById('logsSearch');
    if (logsSearch && logsSearch.value.trim() && !logsSearchTimeout) {
        searchLogs(logsSearch.value.trim());
    }
    
    // Create the log entry and add it to our fragment
    const logEntry = document.createElement('div');
    logEntry.className = 'log-entry';
    logEntry.textContent = logData.message;
    if (!logFragment) {
        logFragment = document.createDocumentFragment();
    }
    logFragment.appendChild(logEntry);
    
    // If we don't have a timer running yet, start one
    if (!batchTimeoutId) {
        batchTimeoutId = setTimeout(flushLogBatch, LOG_BATCH_INTERVAL);
    }
}

function flushLogBatch() {
    if (logFragment.children.length > 0) {
        const logsContainerData = document.getElementById('logsContainerData');
        const autoScrollCheckbox = document.getElementById('autoScrollCheckbox');
        
        // If auto-scroll is checked, we'll always scroll to bottom after adding logs
        const shouldScrollToBottom = autoScrollCheckbox.checked;
        
        // If auto-scroll is not checked, remember current scroll position to maintain it
        const scrollTop = logsContainerData.scrollTop;
        
        // Append all entries at once
        logsContainerData.appendChild(logFragment);
        
        if (shouldScrollToBottom) {
            // Scroll to bottom if auto-scroll is enabled
            logsContainerData.scrollTop = logsContainerData.scrollHeight;
        } else {
            // Maintain scroll position if auto-scroll is disabled
            logsContainerData.scrollTop = scrollTop;
        }
        
        // Create a new empty fragment for the next batch
        logFragment = null;
    }
    
    // Clear the timeout
    batchTimeoutId = null;
}

// Function to flush all event batches
function flushEventBatches() {
    // Process each cluster's event fragment
    for (const clusterName in eventFragments) {
        if (eventFragments[clusterName].children.length > 0) {
            const clusterDiv = document.getElementById(`events-${clusterName}`);
            if (clusterDiv) {
                const eventsContent = clusterDiv.querySelector('.events-content');
                const autoScrollCheckbox = clusterDiv.querySelector('.events-auto-scroll');
                
                // If auto-scroll is checked, we'll always scroll to bottom after adding events
                const shouldScrollToBottom = autoScrollCheckbox.checked;
                
                // If auto-scroll is not checked, remember current scroll position to maintain it
                const scrollTop = eventsContent.scrollTop;
                
                // Append all entries at once
                eventsContent.appendChild(eventFragments[clusterName]);
                
                if (shouldScrollToBottom) {
                    // Scroll to bottom if auto-scroll is enabled
                    eventsContent.scrollTop = eventsContent.scrollHeight;
                } else {
                    // Maintain scroll position if auto-scroll is disabled
                    eventsContent.scrollTop = scrollTop;
                }
                
                // Create a new empty fragment for the next batch
                eventFragments[clusterName] = null;
            }
        }
    }
    
    // Clear the timeout
    eventBatchTimeoutId = null;
}

function initializeEventsPage() {
    const clusterCheckboxes = document.querySelectorAll('.cluster-checkbox');
    clusterCheckboxes.forEach(checkbox => {
        checkbox.addEventListener('change', handleClusterSelection);
        

    });
    
}

function initializeLogsPage() {
    const logsCheckbox = document.getElementById('logsCheckbox');
    if (logsCheckbox) {
        logsCheckbox.addEventListener('change', handleLogsSelection);
    }
    
}
