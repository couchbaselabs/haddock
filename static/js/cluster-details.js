// ==================== CONSTANTS AND GLOBALS ======================
const socket = new WebSocket("ws://" + window.location.host + "/ws");
let currentLogSessionId = null;
let currentEventSessionId = null;
let logFragment = document.createDocumentFragment();
let batchTimeoutId = null;
const LOG_BATCH_INTERVAL = 500;
const EVENT_BATCH_INTERVAL = 10;
const SEARCH_DEBOUNCE_DELAY = 300;

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
    // Get the cluster name from the hidden element
    const clusterNameElement = document.getElementById('clusterNameHolder');
    const clusterName = clusterNameElement.getAttribute('data-name');
    
    initializeControls();
    initializeSearchFunctionality();
    
    // Set up WebSocket event handler
    socket.onmessage = handleWebSocketMessage;
    
    // Add event listener for the Couchbase UI button
    const couchbaseUIBtn = document.getElementById('openCouchbaseUI');
    if (couchbaseUIBtn) {
        couchbaseUIBtn.addEventListener('click', function() {
            window.open(`/cui/${clusterName}/`, '_blank');
        });
    }
});

// ==================== INITIALIZATION FUNCTIONS ==================
function initializeControls() {
    const logsCheckbox = document.getElementById('logsCheckbox');
    const watchEventsCheckbox = document.getElementById('watchEventsCheckbox');
    const followCheckbox = document.getElementById('followCheckbox');
    const autoScrollCheckbox = document.getElementById('autoScrollCheckbox');
    const startTimeInput = document.getElementById('startTime');
    const endTimeInput = document.getElementById('endTime');
    const logsContainerData = document.getElementById('logsContainerData');
    const clusterName = document.getElementById('clusterNameHolder').getAttribute('data-name');

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

    // Watch events checkbox event listener
    watchEventsCheckbox.addEventListener('change', function() {
        const eventsContainerData = document.getElementById('eventsContainerData');
        eventsContainerData.querySelector('.events-content').innerHTML = '';
        currentEventSessionId = this.checked ? generateSessionId() : null;

        socket.send(JSON.stringify({
            type: "clustersevents",
            clusters: this.checked ? [clusterName] : [],
            sessionId: currentEventSessionId
        }));
    });

    // Logs checkbox event listener
    logsCheckbox.addEventListener('change', function() {
        const logsContainerData = document.getElementById('logsContainerData');
        logFragment = document.createDocumentFragment();
        logsContainerData.innerHTML = '';

        if (this.checked) {
            if (!followCheckbox.checked && !startTimeInput.value) {
                alert("Please select a start time when not following logs");
                this.checked = false;
                return;
            }

            currentLogSessionId = generateSessionId();

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
                sessionId: currentLogSessionId,
                follow: followCheckbox.checked,
                startTime: rfcStart,
                endTime: rfcEnd,
                clusterMap: { [clusterName]: true }
            };

            socket.send(JSON.stringify(request));
        } else {
            currentLogSessionId = null;
            if (logsFuse) logsFuse = new Fuse([], logsFuse.options);
            logsContainerData.innerHTML = '';
            socket.send(JSON.stringify({
                type: "logs",
                sessionId: null
            }));
        }
    });

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
        
        if (eventsSearchTimeout) {
            clearTimeout(eventsSearchTimeout);
        }
        
        if (query) {
            eventsContainerData.classList.add('hidden');
            eventsSearchResults.classList.add('active');
            eventsSearchResults.innerHTML = '<div class="search-loading">Searching...</div>';
            
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
        
        if (logsSearchTimeout) {
            clearTimeout(logsSearchTimeout);
        }
        
        if (query) {
            logsContainerData.classList.add('hidden');
            logsSearchResults.classList.add('active');
            logsSearchResults.innerHTML = '<div class="search-loading">Searching...</div>';
            
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

// ==================== EVENT HANDLERS ====================
function handleWebSocketMessage(event) {
    const data = JSON.parse(event.data);
    
    if (data.type === "clusterConditions") {
        renderConditions(data.conditions);
        return;
    }
    
    if (data.type === "event" && data.sessionId === currentEventSessionId) {
        updateEvents(data);
        return;
    }
    
    if (data.type === "log" && data.sessionId === currentLogSessionId) {
        updateLogs(data);
        return;
    }
}

// ==================== SEARCH FUNCTIONS ====================
function initializeFuse() {
    const eventsOptions = {
        threshold: 0.2,
        ignoreLocation: true,
        includeMatches: true,
        minMatchCharLength: 3,
        keys: ['kind', 'objectName', 'message']
    };

    const logsOptions = {
        threshold: 0.2,
        ignoreLocation: true,
        includeMatches: true,
        minMatchCharLength: 3,
    };

    eventsFuse = new Fuse([], eventsOptions);
    logsFuse = new Fuse([], logsOptions);
}

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
        
        let kindHighlighted = event.kind || '';
        let nameHighlighted = event.objectName || '';
        let messageHighlighted = event.message || '';
        
        if (result.matches && result.matches.length > 0) {
            result.matches.forEach(match => {
                if (match.key === 'kind' && match.indices.length > 0) {
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
        const logMessage = result.item;
        const resultDiv = document.createElement('div');
        resultDiv.className = 'search-result-item search-result-log';
        
        let highlightedText = logMessage;
        if (result.matches && result.matches.length > 0) {
            const match = result.matches[0];
            if (match && match.indices.length > 0) {
                highlightedText = highlightMatches(logMessage, match.indices);
            }
        }
        
        resultDiv.innerHTML = highlightedText;
        fragment.appendChild(resultDiv);
    });
    
    logsSearchResults.appendChild(fragment);
    logsSearchResults.scrollTop = 0;
}

// ==================== UPDATE FUNCTIONS ====================
function updateEvents(eventData) {
    if (eventsFuse) {
        eventsFuse.add(eventData);
    }
    
    const eventsSearch = document.getElementById('eventsSearch');
    if (eventsSearch && eventsSearch.value.trim() && !eventsSearchTimeout) {
        searchEvents(eventsSearch.value.trim());
    }
    
    const eventElement = document.createElement("div");
    eventElement.className = 'event-item';
    eventElement.innerHTML = `
        <span class="event-property"><strong>Kind:</strong> ${eventData.kind}</span>
        <span class="event-property"><strong>Name:</strong> ${eventData.objectName}</span>
        <span class="event-property"><strong>Message:</strong> ${eventData.message}</span>
    `;
    
    const eventsContent = document.querySelector('.events-content');
    if (eventsContent) {
        eventsContent.appendChild(eventElement);
        
        const autoScrollCheckbox = document.getElementById('autoScrollCheckbox');
        if (autoScrollCheckbox && autoScrollCheckbox.checked) {
            eventsContent.scrollTop = eventsContent.scrollHeight;
        }
    }
}

function updateLogs(logData) {
    if (logsFuse) {
        logsFuse.add(logData.message);
    }
    
    const logsSearch = document.getElementById('logsSearch');
    if (logsSearch && logsSearch.value.trim() && !logsSearchTimeout) {
        searchLogs(logsSearch.value.trim());
    }
    
    const logEntry = document.createElement('div');
    logEntry.className = 'log-entry';
    logEntry.textContent = logData.message;
    logFragment.appendChild(logEntry);
    
    if (!batchTimeoutId) {
        batchTimeoutId = setTimeout(flushLogBatch, LOG_BATCH_INTERVAL);
    }
}

function flushLogBatch() {
    if (logFragment.children.length > 0) {
        const logsContainerData = document.getElementById('logsContainerData');
        const autoScrollCheckbox = document.getElementById('autoScrollCheckbox');
        
        const shouldScrollToBottom = autoScrollCheckbox.checked;
        const scrollTop = logsContainerData.scrollTop;
        
        logsContainerData.appendChild(logFragment);
        
        if (shouldScrollToBottom) {
            logsContainerData.scrollTop = logsContainerData.scrollHeight;
        } else {
            logsContainerData.scrollTop = scrollTop;
        }
        
        logFragment = document.createDocumentFragment();
    }
    
    batchTimeoutId = null;
}

// ==================== UTILITY FUNCTIONS ====================
function generateSessionId() {
    return Date.now().toString() + window.crypto.getRandomValues(new Uint32Array(1))[0];
}

function highlightMatches(text, matches) {
    if (!matches || !text) return text;
    
    // Sort matches by indices from end to beginning to avoid offset issues
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

// ==================== RENDERING FUNCTIONS ====================
    function renderConditions(allConditions) {
    const clusterName = document.getElementById('clusterNameHolder').getAttribute('data-name');
        const conditions = allConditions[clusterName] || [];
        const conditionsContainer = document.getElementById('conditionsContainer');
        
        if (!conditionsContainer) return;
        
    // Sort conditions by priority
        const sortedConditions = [...conditions].sort((a, b) => {
            const colorA = getConditionColor(a.status, a.type);
            const colorB = getConditionColor(b.status, b.type);
            
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
        
        conditionsContainer.innerHTML = '';
        
        if (sortedConditions.length === 0) {
            conditionsContainer.innerHTML = '<div class="no-conditions">No conditions found for this cluster</div>';
            return;
        }
        
        sortedConditions.forEach(condition => {
            const card = document.createElement('div');
            card.className = `condition-card status-${getConditionColor(condition.status, condition.type)}`;
            
            const transitionTime = condition.lastTransitionTime ? new Date(condition.lastTransitionTime).toLocaleString() : 'Unknown';
            const updateTime = condition.lastUpdateTime ? new Date(condition.lastUpdateTime).toLocaleString() : 'Unknown';
            
            card.innerHTML = `
                <div class="condition-header">
                    <h3>${condition.type}</h3>
                    <span class="condition-status">${condition.status}</span>
                </div>
                <div class="condition-details">
                    <div class="condition-field">
                        <span class="field-label">Reason:</span>
                        <span class="field-value">${condition.reason || 'None'}</span>
                    </div>
                    <div class="condition-field">
                        <span class="field-label">Message:</span>
                        <span class="field-value">${condition.message || 'No message'}</span>
                    </div>
                    <div class="condition-timestamps">
                        <div class="condition-field">
                            <span class="field-label">Last Transition:</span>
                            <span class="field-value">${transitionTime}</span>
                        </div>
                        <div class="condition-field">
                            <span class="field-label">Last Update:</span>
                            <span class="field-value">${updateTime}</span>
                        </div>
                    </div>
                </div>
            `;
            
            conditionsContainer.appendChild(card);
        });
    }

function getConditionColor(status, type) {
    if (status === 'Unknown') {
        return 'grey';
    }
    
    if (status === 'True') {
        switch (type) {
            case 'Available':
            case 'Balanced':
            case 'AutoscaleReady':
            case 'Synchronized':
                return 'green';
            case 'Error':
                return 'red';
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
            case 'ManageConfig':
                return 'blue';
            case 'Hibernating':
                return 'purple';
            default:
                return 'grey';
        }
    }
    
    if (status === 'False') {
        switch (type) {
            case 'Available':
            case 'Balanced':
            case 'AutoscaleReady':
            case 'Synchronized':
                return 'red';
            case 'Error':
                return 'grey';
            default:
                return 'grey';
        }
    }
    
    return 'grey';
} 