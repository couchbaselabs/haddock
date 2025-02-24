const socket = new WebSocket("ws://" + window.location.host + "/ws");
let currentLogSessionId = null;


document.addEventListener('DOMContentLoaded', () => {
    const logsCheckbox = document.getElementById('logsCheckbox'); 
    const clusterCheckboxes = document.querySelectorAll('.cluster-checkbox');
    const followCheckbox = document.getElementById('followCheckbox');
    const startTimeInput = document.getElementById('startTime');
    const endTimeInput = document.getElementById('endTime');

    startTimeInput.value = '';
    endTimeInput.value = '';
    endTimeInput.disabled = followCheckbox.checked;
   

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
        const logsContainer = document.getElementById('logsContainer');
        logsContainer.innerHTML = '';
        
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
        const eventsContainer = document.getElementById("eventsContainer");
        const clusterDivs = eventsContainer.getElementsByClassName('cluster-events');
        
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
                <h2>${eventData.clusterName}</h2>
                <div class="events-content"></div>
            `;
            document.getElementById("eventsContainer").appendChild(clusterDiv);
        }
    
        const eventsContent = clusterDiv.querySelector('.events-content');
        const eventElement = document.createElement("div");
        eventElement.className = 'event-item';
        eventElement.innerHTML = `
            <span class="event-property"><strong>Kind:</strong> ${eventData.kind}</span>
            <span class="event-property"><strong>Name:</strong> ${eventData.objectName}</span>
            <span class="event-property"><strong>Message:</strong> ${eventData.message}</span>
        `;
        eventsContent.appendChild(eventElement);
        eventsContent.scrollTop = eventsContent.scrollHeight;
    }
    
    function updateLogs(logData) {
        const logsContainer = document.getElementById('logsContainer');
        const logEntry = document.createElement('div');
        logEntry.className = 'log-entry';
        logEntry.textContent = logData.message;
        logsContainer.appendChild(logEntry);
        logsContainer.scrollTop = logsContainer.scrollHeight;
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







