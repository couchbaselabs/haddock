const socket = new WebSocket("ws://" + window.location.host + "/ws");

document.getElementById('fetchEventsButton').addEventListener('click', fetchEvents);

function fetchEvents() {
    const selectedClusters = Array.from(document.querySelectorAll('.cluster-checkbox:checked')).map(cb => cb.value);
    if (selectedClusters.length === 0) {
        alert("Please select at least one cluster");
        return;
    }

    // Remove event divs for unselected clusters
    const eventsContainer = document.getElementById("eventsContainer");
    if (!eventsContainer) {
        console.error("Events container not found!");
        return;
    }

    const clusterDivs = eventsContainer.getElementsByClassName('cluster-events');
    //console.log("Found cluster divs:", clusterDivs.length);  // Debug line
    
    Array.from(clusterDivs).forEach(div => {
        const clusterName = div.id.replace('events-', '');
        console.log("Checking cluster:", clusterName, "Selected:", selectedClusters.includes(clusterName));
        if (!selectedClusters.includes(clusterName)) {
            div.remove();
        }
    });

    const message = JSON.stringify({ clusters: selectedClusters });
    //console.log(message);
    socket.send(message);
}

    

socket.onmessage = function(event) {
    const eventData = JSON.parse(event.data);
    if (eventData.type === "clusters") {
        updateClusters(eventData.clusters);
    } else {
        updateEvents(eventData);
    }
};

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