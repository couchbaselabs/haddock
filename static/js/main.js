const socket = new WebSocket("ws://" + window.location.host + "/ws");
let currentLogSessionId = null;
let logFragment = document.createDocumentFragment();
let batchTimeoutId = null;
const BATCH_INTERVAL = 500;


document.addEventListener('DOMContentLoaded', () => {
    const logsCheckbox = document.getElementById('logsCheckbox'); 
    const clusterCheckboxes = document.querySelectorAll('.cluster-checkbox');
    const followCheckbox = document.getElementById('followCheckbox');
    const autoScrollCheckbox = document.getElementById('autoScrollCheckbox');
    const startTimeInput = document.getElementById('startTime');
    const endTimeInput = document.getElementById('endTime');
    const logsContainerData = document.getElementById('logsContainerData');

    startTimeInput.value = '';
    endTimeInput.value = '';
    endTimeInput.disabled = followCheckbox.checked;
   
    // Add event listener for scroll events on logs container
    logsContainerData.addEventListener('scroll', function() {
        // If user scrolls up (away from bottom) and auto-scroll is checked, uncheck it
        if (autoScrollCheckbox.checked) {
            const isScrolledToBottom = logsContainerData.scrollHeight - logsContainerData.clientHeight <= logsContainerData.scrollTop + 50;
            if (!isScrolledToBottom) {
                autoScrollCheckbox.checked = false;
            }
        }
    });

    autoScrollCheckbox.addEventListener('change', function() {
        if (this.checked) {
            logsContainerData.scrollTop = logsContainerData.scrollHeight;
        }
    });
   

    // Event listeners
    clusterCheckboxes.forEach(checkbox => {
        checkbox.addEventListener('change', handleClusterSelection);
    });

    logsCheckbox.addEventListener('change', handleLogsSelection);

    followCheckbox.addEventListener('change', (event) => {
        endTimeInput.disabled = event.target.checked;
        if (event.target.checked) {
            endTimeInput.value = '';
        }
    });

    function handleLogsSelection(event) {
        const logsContainerData = document.getElementById('logsContainerData');
        logsContainerData.innerHTML = '';
        
        if (event.target.checked) {
            // Validate inputs
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
                // Convert local datetime string to RFC3339
                const startDate = new Date(startTimeInput.value);
                rfcStart = startDate.toISOString(); // e.g. "2025-02-25T21:00:00.000Z"
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
                startTime: rfcStart,    // send in RFC3339 now
                endTime: rfcEnd         // send in RFC3339 now
            };

            console.log("request", request);
            socket.send(JSON.stringify(request));
        } else {
            currentLogSessionId = null;
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
    
    function generateSessionId() {
        return Date.now().toString() + Math.random().toString(36).substr(2, 9);
    }
    

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
                // If user scrolls up (away from bottom) and auto-scroll is checked, uncheck it
                if (autoScrollCheckbox.checked) {
                    const isScrolledToBottom = eventsContent.scrollHeight - eventsContent.clientHeight <= eventsContent.scrollTop + 50;
                    if (!isScrolledToBottom) {
                        autoScrollCheckbox.checked = false;
                    }
                }
            });
            
            // Add change event listener to immediately scroll when checked
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
    
        const eventsContent = clusterDiv.querySelector('.events-content');
        const autoScrollCheckbox = clusterDiv.querySelector('.events-auto-scroll');
        const eventElement = document.createElement("div");
        eventElement.className = 'event-item';
        eventElement.innerHTML = `
            <span class="event-property"><strong>Kind:</strong> ${eventData.kind}</span>
            <span class="event-property"><strong>Name:</strong> ${eventData.objectName}</span>
            <span class="event-property"><strong>Message:</strong> ${eventData.message}</span>
        `;
        
        // Remember scroll position if auto-scroll is not checked
        const shouldScrollToBottom = autoScrollCheckbox.checked;
        const scrollTop = eventsContent.scrollTop;
        
        // Add the event
        eventsContent.appendChild(eventElement);
        
        // Scroll accordingly
        if (shouldScrollToBottom) {
            eventsContent.scrollTop = eventsContent.scrollHeight;
        } else {
            eventsContent.scrollTop = scrollTop;
        }
    }
    
    function updateLogs(logData) {
        // Create the log entry and add it to our fragment instead of the DOM
        const logEntry = document.createElement('div');
        logEntry.className = 'log-entry';
        logEntry.textContent = logData.message;
        logFragment.appendChild(logEntry);
        
        // If we don't have a timer running yet, start one
        if (!batchTimeoutId) {
            batchTimeoutId = setTimeout(flushLogBatch, BATCH_INTERVAL);
        }
    }
    
    function flushLogBatch() {
        if (logFragment.children.length > 0) {
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

    socket.onmessage = function(event) {
        const data = JSON.parse(event.data);
        switch (data.type) {
            case "clusters":
                updateClusters(data.clusters);
                break;
            case "event":
                updateEvents(data);
                break;
            case "log":
                //const logsCheckbox = document.getElementById('logsCheckbox');
                if (data.sessionId === currentLogSessionId) {
                    updateLogs(data);
                }
                break;
        }
    };
});







