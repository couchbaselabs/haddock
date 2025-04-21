// Metrics Dashboard Functionality
document.addEventListener('DOMContentLoaded', () => {
    // Initialize filter functionality
    setupMetricsFiltering();
    
    // Initialize metrics if URL hash is #metrics
    if (window.location.hash === '#metrics') {
        startMetricsRefresh();
    }
});

// Global variables
let metricsRefreshInterval = null;
const REFRESH_INTERVAL = 10000; // 10 seconds
const metricPositions = new Map(); // To track metric positions
let metricsFuse = null; // For fuzzy search
let currentMetricsData = []; // Store the current metrics data
const METRICS_SEARCH_DEBOUNCE_DELAY = 300; // Delay in ms for search debounce
let metricsSearchTimeout = null;
const metricCardsCache = new Map(); // Cache to store metric cards by metric name

// Cache DOM references
const DOM = {
    metricsGrid: () => document.querySelector('.metrics-grid'),
    chartsContainer: () => document.querySelector('.charts-container'),
    metricsHeader: () => document.querySelector('.metrics-dashboard .metrics-header'),
    refreshIndicator: () => document.getElementById('refreshIndicator'),
    refreshDot: () => document.querySelector('.refresh-dot'),
    metricsSearch: () => document.getElementById('metricsSearch'),
    metricsType: () => document.getElementById('metricsType'),
    clearMetricsSearch: () => document.getElementById('clearMetricsSearch')
};

// Start metrics refresh interval
function startMetricsRefresh() {
    // Clear any existing interval
    stopMetricsRefresh();
    
    // Initial fetch
    fetchAndDisplayMetrics();
    
    // Set up interval for refresh
    metricsRefreshInterval = setInterval(fetchAndDisplayMetrics, REFRESH_INTERVAL);
    
    // Add refresh indicator
    addRefreshIndicator();
}

// Stop metrics refresh interval
function stopMetricsRefresh() {
    if (metricsRefreshInterval) {
        clearInterval(metricsRefreshInterval);
        metricsRefreshInterval = null;
        
        // Remove refresh indicator
        const refreshIndicator = DOM.refreshIndicator();
        if (refreshIndicator) {
            refreshIndicator.remove();
        }
    }
}

// Add refresh indicator to the header
function addRefreshIndicator() {
    const metricsHeader = DOM.metricsHeader();
    if (metricsHeader && !DOM.refreshIndicator()) {
        const refreshIndicator = document.createElement('div');
        refreshIndicator.id = 'refreshIndicator';
        refreshIndicator.className = 'refresh-indicator';
        refreshIndicator.innerHTML = 'Auto-refreshing <span class="refresh-dot"></span>';
        metricsHeader.appendChild(refreshIndicator);
    }
}

// Initialize Fuse.js for metrics search
function initializeMetricsFuse(metricsData) {
    const searchOptions = {
        threshold: 0.3,
        ignoreLocation: true,
        keys: ['name', 'help', 'type'],
        minMatchCharLength: 2
    };
    
    // Create searchable items from all metrics
    const searchItems = [];
    
    // Process simple metrics
    for (const name in metricsData.simpleMetrics) {
        const metric = metricsData.simpleMetrics[name];
        searchItems.push({
            name: metric.name,
            help: metric.help || '',
            type: metric.type,
            category: 'simple',
            originalData: metric
        });
    }
    
    // Initialize Fuse with the search items
    metricsFuse = new Fuse(searchItems, searchOptions);
    
    // Store the current data
    currentMetricsData = searchItems;
}

// Fetch metrics from the server and display them
async function fetchAndDisplayMetrics() {
    try {
        // Fetch metrics in JSON format
        const response = await fetch('/metrics', {
            headers: { 'Accept': 'application/json' }
        });
        
        if (!response.ok) {
            throw new Error(`Error fetching metrics: ${response.status}`);
        }
        
        const metricsData = await response.json();
        
        // Show refresh animation
        pulseRefreshIndicator();
        
        // Process and organize metrics
        const organizedMetrics = organizeMetrics(metricsData);
        
        // Initialize fuzzy search
        initializeMetricsFuse(organizedMetrics);
        
        // Check if search is active
        const searchInput = DOM.metricsSearch();
        if (searchInput && searchInput.value.trim()) {
            // If we're searching, show search results instead
            searchMetrics(searchInput.value.trim());
        } else {
            // Otherwise render metrics as usual
            renderMetricCards(organizedMetrics.simpleMetrics);
            
            // Add placeholders for histograms and summaries
            renderHistogramSummaryPlaceholders();
            
            // Apply any active type filters
            const typeSelect = DOM.metricsType();
            if (typeSelect && typeSelect.value !== 'all') {
                filterMetrics();
            }
        }
    } catch (error) {
        console.error('Failed to load metrics:', error);
        showErrorMessage(error.message);
    }
}

// Add placeholders for histograms and summaries
function renderHistogramSummaryPlaceholders() {
    const chartsContainer = DOM.chartsContainer();
    if (!chartsContainer) return;
    
    // Clear existing content
    chartsContainer.innerHTML = '';
    
    // Add placeholders
    const histogramPlaceholder = document.createElement('div');
    histogramPlaceholder.className = 'coming-soon-container';
    histogramPlaceholder.innerHTML = `
        <h3 class="coming-soon-title">Histograms</h3>
        <p class="coming-soon-description">Histogram visualizations will be available in a future update.</p>
    `;
    chartsContainer.appendChild(histogramPlaceholder);
    
    const summaryPlaceholder = document.createElement('div');
    summaryPlaceholder.className = 'coming-soon-container';
    summaryPlaceholder.innerHTML = `
        <h3 class="coming-soon-title">Summaries</h3>
        <p class="coming-soon-description">Summary visualizations will be available in a future update.</p>
    `;
    chartsContainer.appendChild(summaryPlaceholder);
}

// Search metrics using Fuse.js
function searchMetrics(query) {
    if (!metricsFuse || !query) return;
    
    const results = metricsFuse.search(query);
    
    // Get simple metrics from results
    const simpleMetrics = {};
    
    results.forEach(result => {
        const item = result.item;
        
        if (item.category === 'simple') {
            simpleMetrics[item.name] = item.originalData;
        }
    });
    
    // Render the filtered results
    if (Object.keys(simpleMetrics).length > 0) {
        renderMetricCards(simpleMetrics);
        // Add placeholders
        renderHistogramSummaryPlaceholders();
    } else {
        // Show no results message
        DOM.metricsGrid().innerHTML = '<div class="no-results">No matching metrics found</div>';
        DOM.chartsContainer().innerHTML = '';
    }
}

// Show pulse animation on the refresh indicator
function pulseRefreshIndicator() {
    const refreshDot = DOM.refreshDot();
    if (refreshDot) {
        refreshDot.classList.add('pulse');
        setTimeout(() => refreshDot.classList.remove('pulse'), 1000);
    }
}

// Show error message in the metrics grid
function showErrorMessage(message) {
    DOM.metricsGrid().innerHTML = `
        <div class="error-message">
            Failed to load metrics: ${message}
        </div>
    `;
}

// Organize metrics by type from the prom2json format
function organizeMetrics(metricsData) {
    const result = {
        histograms: [],
        summaries: [],
        simpleMetrics: {}
    };
    
    // Process each metric family
    for (const metricFamily of metricsData) {
        const metric = {
            name: metricFamily.name,
            help: metricFamily.help || '',
            type: metricFamily.type,
            values: []
        };
        
        // Process based on metric type
        switch (metricFamily.type) {
            case 'HISTOGRAM':
                // Just collecting for future implementation
                result.histograms.push(metric);
                break;
            case 'SUMMARY':
                // Just collecting for future implementation
                result.summaries.push(metric);
                break;
            case 'COUNTER':
            case 'GAUGE':
            case 'UNTYPED':
                processSimpleMetric(metricFamily, metric, result);
                break;
        }
    }
    
    return result;
}

// Process simple metrics (counters, gauges, untyped)
function processSimpleMetric(metricFamily, metric, result) {
    if (!metricFamily.metrics || metricFamily.metrics.length === 0) return;
    
    metricFamily.metrics.forEach(m => {
        metric.values.push({
            labels: m.labels || {},
            value: parseFloat(m.value)
        });
    });
    
    if (metric.values.length > 0) {
        result.simpleMetrics[metric.name] = metric;
    }
}

// Render metric cards for simple metrics
function renderMetricCards(simpleMetrics) {
    const metricsGrid = DOM.metricsGrid();
    if (!metricsGrid) return;
    
    const fragment = document.createDocumentFragment();
    const metricStillExists = new Set();
    const orderedElements = [];
    
    // Store metrics and positions for ordering
    const currentMetrics = [];
    for (const metricName in simpleMetrics) {
        const position = metricPositions.has(metricName) ? 
            metricPositions.get(metricName) : 
            metricPositions.size;
        
        currentMetrics.push({
            name: metricName,
            position: position,
            metric: simpleMetrics[metricName]
        });
        
        // Record position for future renders
        metricPositions.set(metricName, position);
    }
    
    // Sort metrics by their position to maintain order
    currentMetrics.sort((a, b) => a.position - b.position);
    
    // Create or update cards in position order
    currentMetrics.forEach(({name, metric}) => {
        let card;
        metricStillExists.add(name);
        
        if (metricCardsCache.has(name)) {
            // Reuse existing card from cache
            card = metricCardsCache.get(name);
            // Update values
            updateMetricCardValues(card, metric);
        } else {
            // Create new card for new metrics
            card = createMetricCard(metric);
            // Add to cache
            metricCardsCache.set(name, card);
        }
        
        // Add to ordered elements array
        orderedElements.push(card);
    });
    
    // Remove cards for metrics that no longer exist from the cache
    for (const [metricName, cardElement] of metricCardsCache.entries()) {
        if (!metricStillExists.has(metricName)) {
            metricCardsCache.delete(metricName);
        }
    }
    
    if (orderedElements.length === 0) {
        metricsGrid.innerHTML = '<div class="no-results">No metrics found</div>';
        return;
    }

    // Update the metrics grid with ordered elements
    orderedElements.forEach(elem => fragment.appendChild(elem));
    metricsGrid.innerHTML = '';
    metricsGrid.appendChild(fragment);
}

// Create a new metric card
function createMetricCard(metric) {
    // Create card container
    const card = document.createElement('div');
    card.className = `metric-card ${metric.type.toLowerCase() || 'unknown'}-card`;
    card.setAttribute('data-metric-type', metric.type.toLowerCase() || 'unknown');
    card.setAttribute('data-metric-name', metric.name);
    
    // Create header with title and type
    const metricHeader = document.createElement('div');
    metricHeader.className = 'metric-header';
    
    const metricTitle = document.createElement('h3');
    metricTitle.className = 'metric-name';
    metricTitle.textContent = metric.name;
    
    // Check if the title will be truncated and add truncated class if needed
    setTimeout(() => {
        if (metricTitle.scrollWidth > metricTitle.clientWidth) {
            metricTitle.classList.add('truncated');
        }
    }, 100);
    
    const metricType = document.createElement('span');
    metricType.className = `metric-type ${metric.type.toLowerCase() || 'unknown'}`;
    metricType.textContent = metric.type || 'unknown';
    
    metricHeader.appendChild(metricTitle);
    metricHeader.appendChild(metricType);
    card.appendChild(metricHeader);
    
    // Add help text if available
    if (metric.help) {
        const helpText = document.createElement('div');
        helpText.className = 'metric-help';
        helpText.textContent = metric.help;
        card.appendChild(helpText);
    }
    
    // Create container for metric values
    const valuesContainer = document.createElement('div');
    valuesContainer.className = 'metric-values-container';
    
    // Create entries for each value/label combination
    if (metric.values && metric.values.length > 0) {
        metric.values.forEach((valueObj) => {
            const entry = createMetricValueEntry(valueObj);
            valuesContainer.appendChild(entry);
        });
    }
    
    card.appendChild(valuesContainer);
    
    return card;
}

// Create an entry for a single metric value with its labels
function createMetricValueEntry(valueObj) {
    const entry = document.createElement('div');
    entry.className = 'metric-entry';
    
    // Create unique ID for this entry based on labels
    const entryId = generateEntryId(valueObj.labels);
    entry.setAttribute('data-entry-id', entryId);
    
    // Create content wrapper for better alignment
    const entryContent = document.createElement('div');
    entryContent.className = 'metric-entry-content';
    
    // Labels wrapper
    const labelsWrapper = document.createElement('div');
    labelsWrapper.className = 'metric-labels-wrapper';
    
    // Add labels section if there are labels
    if (Object.keys(valueObj.labels).length > 0) {
        const labelsContainer = document.createElement('div');
        labelsContainer.className = 'metric-labels';
        
        for (const [key, value] of Object.entries(valueObj.labels)) {
            const label = document.createElement('div');
            label.className = 'metric-label';
            label.innerHTML = `<span class="metric-label-key">${key}</span>: ${value}`;
            labelsContainer.appendChild(label);
        }
        
        labelsWrapper.appendChild(labelsContainer);
    } else {
        // For metrics without labels, add a placeholder
        const noLabelsText = document.createElement('div');
        noLabelsText.className = 'metric-labels';
        noLabelsText.innerHTML = '<div class="metric-label">No labels</div>';
        labelsWrapper.appendChild(noLabelsText);
    }
    
    // Value wrapper
    const valueWrapper = document.createElement('div');
    valueWrapper.className = 'metric-value-wrapper';
    
    // Add value
    const valueDisplay = document.createElement('div');
    valueDisplay.className = 'metric-value';
    valueDisplay.textContent = formatMetricValue(valueObj.value, valueObj.labels);
    valueWrapper.appendChild(valueDisplay);
    
    // Add both to content wrapper
    entryContent.appendChild(labelsWrapper);
    entryContent.appendChild(valueWrapper);
    
    // Add content wrapper to entry
    entry.appendChild(entryContent);
    
    return entry;
}

// Generate a unique ID for an entry based on its labels
function generateEntryId(labels) {
    if (!labels || Object.keys(labels).length === 0) {
        return 'no-labels';
    }
    
    return Object.entries(labels)
        .sort(([keyA], [keyB]) => keyA.localeCompare(keyB))
        .map(([key, value]) => `${key}:${value}`)
        .join(',');
}

// Update all values in a metric card
function updateMetricCardValues(card, metric) {
    if (!metric.values || metric.values.length === 0) return;
    
    const valuesContainer = card.querySelector('.metric-values-container');
    if (!valuesContainer) return;
    
    // Keep track of entries we've updated
    const updatedEntries = new Set();
    
    // Update existing entries and create new ones as needed
    metric.values.forEach(valueObj => {
        const entryId = generateEntryId(valueObj.labels);
        updatedEntries.add(entryId);
        
        // Find existing entry
        let entry = valuesContainer.querySelector(`[data-entry-id="${entryId}"]`);
        
        if (entry) {
            // Update existing entry
            updateMetricValueEntry(entry, valueObj);
        } else {
            // Create new entry
            entry = createMetricValueEntry(valueObj);
            valuesContainer.appendChild(entry);
        }
    });
    
    // Remove entries that no longer exist
    valuesContainer.querySelectorAll('.metric-entry').forEach(entry => {
        const entryId = entry.getAttribute('data-entry-id');
        if (!updatedEntries.has(entryId)) {
            entry.remove();
        }
    });
}

// Update a single metric value entry
function updateMetricValueEntry(entry, valueObj) {
    const valueDisplay = entry.querySelector('.metric-value');
    if (!valueDisplay) return;
    
    const newValue = formatMetricValue(valueObj.value, valueObj.labels);
    
    if (valueDisplay.textContent !== newValue) {
        valueDisplay.textContent = newValue;
        // Add flash animation
        valueDisplay.classList.remove('value-updated');
        // Force a reflow to restart the animation
        void valueDisplay.offsetWidth;
        valueDisplay.classList.add('value-updated');
    }
}

// Format metric values for display with appropriate units
function formatMetricValue(value, labels) {
    if (typeof value !== 'number') return value.toString();
    
    // Check for byte metrics using labels and metric name
    if (labels && (
        labels.unit === 'bytes' || 
        (typeof labels.metric_name === 'string' && 
         isLikelyBytes(labels.metric_name))
    )) {
        return formatBytes(value);
    }
    
    // Format numbers with appropriate notation
    return formatNumber(value, Number.isInteger(value) ? 0 : 3);
}

// Check if metric name suggests bytes
function isLikelyBytes(metricName) {
    return metricName.includes('_bytes') || 
           metricName.includes('_size') || 
           metricName.includes('_capacity') || 
           metricName.includes('_memory');
}

// Format bytes with appropriate unit
function formatBytes(bytes) {
    if (bytes === 0) return '0 Bytes';
    
    const k = 1024;
    const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB', 'PB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    
    // Don't go beyond available units
    const unit = i < sizes.length ? sizes[i] : sizes[sizes.length - 1];
    const value = bytes / Math.pow(k, i);
    
    // Format with appropriate decimal places
    return `${i === 0 ? value : value.toFixed(2)} ${unit}`;
}

// Format numbers with scientific notation for large values
function formatNumber(num, decimalPlaces = 0) {
    // For very large or very small numbers, use scientific notation
    if (Math.abs(num) >= 1000000 || (Math.abs(num) < 0.001 && num !== 0)) {
        return num.toExponential(2);
    } else if (decimalPlaces > 0) {
        return parseFloat(num.toFixed(decimalPlaces)).toLocaleString();
    } else {
        return num.toLocaleString();
    }
}

// Setup filtering for metrics
function setupMetricsFiltering() {
    const typeSelect = DOM.metricsType();
    const searchInput = DOM.metricsSearch();
    
    if (typeSelect) {
        typeSelect.addEventListener('change', filterMetrics);
    }
    
    if (searchInput) {
        // Add search functionality
        searchInput.addEventListener('input', () => {
            const query = searchInput.value.trim();
            
            // Clear any previous timeout
            if (metricsSearchTimeout) {
                clearTimeout(metricsSearchTimeout);
            }
            
            // Set a new timeout for debouncing
            metricsSearchTimeout = setTimeout(() => {
                if (query) {
                    searchMetrics(query);
                } else {
                    // If search is cleared, re-fetch and display all metrics
                    fetchAndDisplayMetrics();
                }
            }, METRICS_SEARCH_DEBOUNCE_DELAY);
        });
        
        // Add clear button functionality if it exists
        const clearSearchBtn = DOM.clearMetricsSearch();
        if (clearSearchBtn) {
            clearSearchBtn.addEventListener('click', () => {
                searchInput.value = '';
                fetchAndDisplayMetrics();
            });
        }
    }
}

// Filter metrics based on type
function filterMetrics() {
    const typeSelect = DOM.metricsType();
    
    if (!typeSelect) return;
    
    const filterType = typeSelect.value.toLowerCase();
    
    // If "all" is selected, show everything
    if (filterType === 'all') {
        document.querySelectorAll('.metric-card, .coming-soon-container').forEach(el => {
            el.style.display = 'block';
        });
        return;
    }
    
    // Filter metric cards
    document.querySelectorAll('.metric-card').forEach(card => {
        const cardType = card.getAttribute('data-metric-type');
        card.style.display = cardType === filterType ? 'block' : 'none';
    });
    
    // Show or hide placeholders based on filter
    document.querySelectorAll('.coming-soon-container').forEach(container => {
        const title = container.querySelector('.coming-soon-title').textContent;
        
        if ((title === 'Histograms' && filterType === 'histogram') || 
            (title === 'Summaries' && filterType === 'summary')) {
            container.style.display = 'block';
        } else {
            container.style.display = 'none';
        }
    });
} 