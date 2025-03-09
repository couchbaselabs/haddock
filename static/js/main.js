// ==================== CONSTANTS AND GLOBALS ======================
const socket = new WebSocket("ws://" + window.location.host + "/ws");
let currentLogSessionId = null;
let logFragment = document.createDocumentFragment();
let batchTimeoutId = null;
let eventFragments = {}; // Map to store event fragments by cluster name
let eventBatchTimeoutId = null;
const LOG_BATCH_INTERVAL = 500; // For logs
const EVENT_BATCH_INTERVAL = 10; // Faster interval for events
const SEARCH_DEBOUNCE_DELAY = 300; // Delay in ms for search debounce

// Search data storage
let eventsData = [];
let logsData = [];
let eventsFuse = null;
let logsFuse = null;

// Search functionality
let eventsSearchTimeout = null;
let logsSearchTimeout = null;

// ==================== DOCUMENT READY EVENT =======================
document.addEventListener('DOMContentLoaded', () => {
    initializeControls();
    initializeSearchFunctionality();
    initializeCouchbaseUINavigation();
    
    // Set up WebSocket event handler
    socket.onmessage = handleWebSocketMessage;
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
    logFragment = document.createDocumentFragment();
    logsContainerData.innerHTML = '';

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

        const request = {
            type: "logs",
            enabled: true,
            sessionId: currentLogSessionId,
            follow: followCheckbox.checked,
            startTime: rfcStart,
            endTime: rfcEnd
        };

        socket.send(JSON.stringify(request));
    } else {
        currentLogSessionId = null;
        // Reset Fuse collection
        if (logsFuse) logsFuse = new Fuse([], logsFuse.options);
        logsContainerData.innerHTML = ''; // Clear logs container visually
        socket.send(JSON.stringify({
            type: "logs",
            enabled: false,
            sessionId: null
        }));
    }
}

function handleClusterSelection() {
    const selectedClusters = Array.from(document.querySelectorAll('.cluster-checkbox:checked')).map(cb => cb.value);
    
    // Remove event divs for unselected clusters
    const eventsContainerData = document.getElementById("eventsContainerData");
    const clusterDivs = eventsContainerData.getElementsByClassName('cluster-events');
    
    Array.from(clusterDivs).forEach(div => {
        const clusterName = div.id.replace('events-', '');
        if (!selectedClusters.includes(clusterName)) {
            div.remove();
        }
    });

    socket.send(JSON.stringify({
        type: "clusters",
        clusters: selectedClusters
    }));
}

function handleWebSocketMessage(event) {
    const data = JSON.parse(event.data);
    switch (data.type) {
        case "clusters":
            updateClusters(data.clusters);
            break;
        case "event":
            updateEvents(data);
            break;
        case "log":
            if (data.sessionId === currentLogSessionId) {
                updateLogs(data);
            }
            break;
        case "clusterConditions":
            renderClusterTiles(data.conditions);
            break;
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
        resultDiv.className = 'search-result-item search-result-event';
        
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
            <span class="event-property"><strong>Kind:</strong> ${kindHighlighted}</span>
            <span class="event-property"><strong>Name:</strong> ${nameHighlighted}</span>
            <span class="event-property"><strong>Message:</strong> ${messageHighlighted}</span>
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
    const container = document.getElementById("clustersContainer");

    // Add new clusters
    clusters.forEach(cluster => {
        if (!document.getElementById(`cluster-${cluster}`)) {
            const label = document.createElement("label");
            label.id = `cluster-${cluster}`;
            label.className = "cluster-label";
            label.innerHTML = `
                <input type="checkbox" class="cluster-checkbox" value="${cluster}">
                <span class="cluster-name">${cluster}</span>
            `;
            container.appendChild(label);
            
            // Add event listener to new checkbox
            const checkbox = label.querySelector('.cluster-checkbox');
            checkbox.addEventListener('change', handleClusterSelection);
        }
    });

    // Remove old clusters
    const existingLabels = container.getElementsByClassName('cluster-label');
    Array.from(existingLabels).forEach(label => {
        const cluster = label.querySelector('.cluster-checkbox').value;
        if (!clusters.includes(cluster)) {
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
    }

    // Create an event element
    const eventElement = document.createElement("div");
    eventElement.className = 'event-item';
    eventElement.innerHTML = `
        <span class="event-property"><strong>Kind:</strong> ${eventData.kind}</span>
        <span class="event-property"><strong>Name:</strong> ${eventData.objectName}</span>
        <span class="event-property"><strong>Message:</strong> ${eventData.message}</span>
    `;
    
    // Initialize fragment for this cluster if it doesn't exist
    if (!eventFragments[eventData.clusterName]) {
        eventFragments[eventData.clusterName] = document.createDocumentFragment();
    }
    
    // Add the event to the fragment
    eventFragments[eventData.clusterName].appendChild(eventElement);
    
    // If we don't have a timer running yet, start one
    if (!eventBatchTimeoutId) {
        eventBatchTimeoutId = setTimeout(flushEventBatches, EVENT_BATCH_INTERVAL);
    }
}

function updateLogs(logData) {
    // Add the log message directly to Fuse index
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
        logFragment = document.createDocumentFragment();
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
                eventFragments[clusterName] = document.createDocumentFragment();
            }
        }
    }
    
    // Clear the timeout
    eventBatchTimeoutId = null;
}

// ==================== UTILITY FUNCTIONS ====================
function generateSessionId() {
    return Date.now().toString() + window.crypto.getRandomValues(new Uint32Array(1))[0];
}

function highlightMatches(text, matches) {
    if (!matches || !text) return text;
    
    // Sort matches by indices from end to beginning
    const sortedMatches = [...matches].sort((a, b) => b[0] - a[0]);
    
    let result = text;
    
    // Process each match from end to beginning
    for (const [start, end] of sortedMatches) {
        const matchedText = result.substring(start, end + 1);
        const highlighted = `<span class="match-highlight">${matchedText}</span>`;
        result = result.substring(0, start) + highlighted + result.substring(end + 1);
    }
    
    return result;
}

// Initialize the Couchbase UI navigation controls
function initializeCouchbaseUINavigation() {
    const clusterSelect = document.getElementById('couchbaseUIClusterSelect');
    const navigateButton = document.getElementById('couchbaseUINavigateBtn');
    
    navigateButton.addEventListener('click', () => {
        const selectedCluster = clusterSelect.value;
        if (selectedCluster) {
            // Open the Couchbase UI in a new tab
            window.open(`/cui/${selectedCluster}/`, '_blank');
        } else {
            alert('Please select a cluster first');
        }
    });
}

// Function to determine the tile color based on status and type
function getTileColor(status, type) {
  // Unknown status takes precedence
  if (status === 'Unknown') {
    return 'grey';
  }

  // Active conditions (status True)
  if (status === 'True') {
    switch (type) {
      // Positive conditions
      case 'Available':
      case 'Balanced':
      case 'AutoscaleReady':
      case 'Synchronized':
        return 'green';

      // Active error condition is bad
      case 'Error':
        return 'red';

      // Transitional/in-progress conditions
      case 'Scaling':
      case 'ScalingUp':
      case 'ScalingDown':
      case 'Upgrading':
      case 'WaitingBetweenMigrations':
      case 'Migrating':
      case 'Rebalancing':
      case 'ExpandingVolume':
      case 'BucketMigrating':
        return 'orange';

      // Special cases
      case 'ManageConfig':
        return 'blue';
      case 'Hibernating':
        return 'purple';

      default:
        return 'grey';  // fallback for any unexpected type
    }
  }
  
  // Inactive conditions (status False)
  if (status === 'False') {
    switch (type) {
      // When healthy conditions are false, that's bad
      case 'Available':
      case 'Balanced':
      case 'AutoscaleReady':
      case 'Synchronized':
        return 'red';

      // For an inactive error, use grey to indicate neutrality instead of green
      case 'Error':
        return 'grey';

      // For other cases, a neutral color can be used
      default:
        return 'grey';
    }
  }
}

// Function to determine the overall tile color based on conditions
function getOverallTileColor(conditions) {
  if (!conditions || conditions.length === 0) {
    return 'grey';
  }
  
  // Priority order for overall status
  const redCondition = conditions.find(c => 
    getTileColor(c.status, c.type) === 'red');
  if (redCondition) return 'red';
  
  const orangeCondition = conditions.find(c => 
    getTileColor(c.status, c.type) === 'orange');
  if (orangeCondition) return 'orange';
  
  const purpleCondition = conditions.find(c => 
    getTileColor(c.status, c.type) === 'purple');
  if (purpleCondition) return 'purple';
  
  const blueCondition = conditions.find(c => 
    getTileColor(c.status, c.type) === 'blue');
  if (blueCondition) return 'blue';
  
  const greenCondition = conditions.find(c => 
    getTileColor(c.status, c.type) === 'green');
  if (greenCondition) return 'green';
  
  return 'grey';
}

// Function to render cluster tiles
function renderClusterTiles(clusterConditions) {
  const container = document.getElementById('clusterTilesContainer');
  if (!container || !clusterConditions) return;
  
  container.innerHTML = '';
  
  Object.keys(clusterConditions).forEach(clusterName => {
    const conditions = clusterConditions[clusterName];
    const overallColor = getOverallTileColor(conditions);
    
    const tile = document.createElement('div');
    tile.className = `cluster-tile tile-${overallColor}`;
    tile.dataset.clusterName = clusterName;
    
    // Add click event to navigate to cluster page
    tile.addEventListener('click', () => {
      window.location.href = `/cluster/${clusterName}`;
    });
    
    const titleEl = document.createElement('h3');
    titleEl.textContent = clusterName;
    tile.appendChild(titleEl);
    
    const conditionsList = document.createElement('ul');
    conditionsList.className = 'conditions-list';
    
    // Sort conditions by priority (error conditions first, then in-progress, then others)
    const sortedConditions = [...conditions].sort((a, b) => {
      const colorA = getTileColor(a.status, a.type);
      const colorB = getTileColor(b.status, b.type);
      
      const priority = {
        'red': 1,
        'orange': 2,
        'purple': 3,
        'blue': 4,
        'green': 5,
        'grey': 6
      };
      
      return priority[colorA] - priority[colorB];
    });
    
    // Display up to 5 most important conditions
    sortedConditions.slice(0, 5).forEach(condition => {
      const conditionColor = getTileColor(condition.status, condition.type);
      
      const conditionItem = document.createElement('li');
      conditionItem.className = 'condition-item';
      
      const conditionType = document.createElement('span');
      conditionType.className = 'condition-type';
      conditionType.textContent = condition.type;
      
      const conditionStatus = document.createElement('span');
      conditionStatus.className = `condition-status status-${conditionColor}`;
      conditionStatus.textContent = condition.status;
      
      conditionItem.appendChild(conditionType);
      conditionItem.appendChild(conditionStatus);
      conditionsList.appendChild(conditionItem);
    });
    
    tile.appendChild(conditionsList);
    container.appendChild(tile);
  });
}
