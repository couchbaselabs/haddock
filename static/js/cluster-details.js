// Wait for DOM content to be fully loaded
document.addEventListener('DOMContentLoaded', function() {
    // Get the cluster name from the hidden element
    const clusterNameElement = document.getElementById('clusterNameHolder');
    const clusterName = clusterNameElement.getAttribute('data-name');
    
    // WebSocket initialization
    const socket = new WebSocket("ws://" + window.location.host + "/ws");
    
    // When the socket is opened
    socket.onopen = function() {
        console.log("WebSocket connection established");
    };
    
    // Handle incoming messages
    socket.onmessage = function(event) {
        const data = JSON.parse(event.data);
        if (data.type === "clusterConditions") {
            renderConditions(data.conditions);
        }
    };
    
    // Add event listener for the Couchbase UI button
    const couchbaseUIBtn = document.getElementById('openCouchbaseUI');
    if (couchbaseUIBtn) {
        couchbaseUIBtn.addEventListener('click', function() {
            window.open(`/cui/${clusterName}/`, '_blank');
        });
    }

    // Render conditions on the page
    function renderConditions(allConditions) {
        const conditions = allConditions[clusterName] || [];
        const conditionsContainer = document.getElementById('conditionsContainer');
        
        if (!conditionsContainer) return;
        
        // Sort conditions by priority (error conditions first, then in-progress, then others)
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
        
        // Clear existing conditions
        conditionsContainer.innerHTML = '';
        
        // No conditions message
        if (sortedConditions.length === 0) {
            conditionsContainer.innerHTML = '<div class="no-conditions">No conditions found for this cluster</div>';
            return;
        }
        
        // Create conditions cards
        sortedConditions.forEach(condition => {
            const card = document.createElement('div');
            card.className = `condition-card status-${getConditionColor(condition.status, condition.type)}`;
            
            // Format timestamps
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
});

// Function to determine the condition card color based on status and type
function getConditionColor(status, type) {
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
    
    return 'grey'; // Default fallback
} 