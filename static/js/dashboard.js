// dashboard.js - Dashboard specific functionality
import { socket } from './core.js';

// Function to determine the tile color based on status and type
export function getTileColor(status, type) {
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
export function getOverallTileColor(conditions) {
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
export function renderClusterTiles(clusterConditions) {
  const container = document.getElementById('clusterTilesContainer');
  if (!container || !clusterConditions) return;
  
  container.innerHTML = ''; // Clear previous tiles

  if (Object.keys(clusterConditions).length === 0) {
    container.innerHTML = '<p class="no-clusters-message">No clusters to display</p>';
    return;
  }
  
  Object.keys(clusterConditions).forEach(clusterName => {
    const conditions = clusterConditions[clusterName];
    const overallColor = getOverallTileColor(conditions);
    
    const tile = document.createElement('div');
    tile.className = `cluster-tile tile-${overallColor}`;
    tile.dataset.clusterName = clusterName;
    
    // Add click event to navigate to cluster page
    tile.addEventListener('click', () => {
      window.open(`/cluster/${clusterName}`, '_blank');
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