// Metrics Dashboard Functionality
document.addEventListener('DOMContentLoaded', () => {
    // Initialize filter functionality
    setupMetricsFiltering();
    
    // Initialize metrics if URL hash is #metrics
    if (window.location.hash === '#metrics') {
        startMetricsRefresh();
    }
    
    // Close tooltips when clicking elsewhere on the page
    document.addEventListener('click', (e) => {
        if (!e.target.closest('.metric-card') && !e.target.closest('.chart-wrapper')) {
            document.querySelectorAll('.tooltip-active').forEach(el => {
                el.classList.remove('tooltip-active');
            });
        }
    });
});

// Global variables
let metricsRefreshInterval = null;
const REFRESH_INTERVAL = 10000; // 10 seconds
const metricPositions = new Map(); // To track metric positions
let metricsFuse = null; // For fuzzy search
let currentMetricsData = []; // Store the current metrics data
const METRICS_SEARCH_DEBOUNCE_DELAY = 300; // Delay in ms for search debounce
let metricsSearchTimeout = null;

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
        const refreshIndicator = document.getElementById('refreshIndicator');
        if (refreshIndicator) {
            refreshIndicator.remove();
        }
    }
}

// Add refresh indicator to the header
function addRefreshIndicator() {
    const metricsHeader = document.querySelector('.metrics-dashboard .metrics-header');
    if (metricsHeader && !document.getElementById('refreshIndicator')) {
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
    
    // Process histograms
    metricsData.histograms.forEach(metric => {
        searchItems.push({
            name: metric.name,
            help: metric.help || '',
            type: 'HISTOGRAM',
            category: 'histogram',
            originalData: metric
        });
    });
    
    // Process summaries
    metricsData.summaries.forEach(metric => {
        searchItems.push({
            name: metric.name,
            help: metric.help || '',
            type: 'SUMMARY',
            category: 'summary',
            originalData: metric
        });
    });
    
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
        const searchInput = document.getElementById('metricsSearch');
        if (searchInput && searchInput.value.trim()) {
            // If we're searching, show search results instead
            searchMetrics(searchInput.value.trim());
        } else {
            // Otherwise render metrics as usual
            renderMetricCards(organizedMetrics.simpleMetrics);
            renderGraphMetrics(organizedMetrics.histograms, organizedMetrics.summaries);
            
            // Apply any active type filters
            const typeSelect = document.getElementById('metricsType');
            if (typeSelect && typeSelect.value !== 'all') {
                filterMetrics();
            }
        }
    } catch (error) {
        console.error('Failed to load metrics:', error);
        showErrorMessage(error.message);
    }
}

// Search metrics using Fuse.js
function searchMetrics(query) {
    if (!metricsFuse || !query) return;
    
    const results = metricsFuse.search(query);
    
    // Separate results by category
    const simpleMetrics = {};
    const histograms = [];
    const summaries = [];
    
    results.forEach(result => {
        const item = result.item;
        
        if (item.category === 'simple') {
            simpleMetrics[item.name] = item.originalData;
        } else if (item.category === 'histogram') {
            histograms.push(item.originalData);
        } else if (item.category === 'summary') {
            summaries.push(item.originalData);
        }
    });
    
    // Render the filtered results
    if (Object.keys(simpleMetrics).length > 0 || histograms.length > 0 || summaries.length > 0) {
        renderMetricCards(simpleMetrics);
        renderGraphMetrics(histograms, summaries);
    } else {
        // Show no results message
        document.querySelector('.metrics-grid').innerHTML = '<div class="no-results">No matching metrics found</div>';
        document.querySelector('.charts-container').innerHTML = '';
    }
}

// Show pulse animation on the refresh indicator
function pulseRefreshIndicator() {
    const refreshDot = document.querySelector('.refresh-dot');
    if (refreshDot) {
        refreshDot.classList.add('pulse');
        setTimeout(() => refreshDot.classList.remove('pulse'), 1000);
    }
}

// Show error message in the metrics grid
function showErrorMessage(message) {
    document.querySelector('.metrics-grid').innerHTML = `
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
                processHistogram(metricFamily, metric, result);
                break;
            case 'SUMMARY':
                processSummary(metricFamily, metric, result);
                break;
            case 'COUNTER':
            case 'GAUGE':
            case 'UNTYPED':
                processSimpleMetric(metricFamily, metric, result);
                break;
        }
    }
    
    // Sort for consistent display order
    result.histograms.sort((a, b) => a.name.localeCompare(b.name));
    result.summaries.sort((a, b) => a.name.localeCompare(b.name));
    
    return result;
}

// Process histogram metrics
function processHistogram(metricFamily, metric, result) {
    const histograms = metricFamily.metrics?.filter(m => m.buckets) || [];
    if (histograms.length === 0) return;
    
    const hm = histograms[0];
    
    // Process classic histograms (object buckets)
    if (typeof hm.buckets === 'object' && !Array.isArray(hm.buckets)) {
        metric.buckets = Object.entries(hm.buckets)
            .map(([upperBound, count]) => ({
                le: upperBound === '+Inf' ? Infinity : parseFloat(upperBound),
                value: parseFloat(count)
            }))
            .sort((a, b) => a.le - b.le);
    } 
    // Process native histograms (array buckets)
    else if (Array.isArray(hm.buckets)) {
        metric.buckets = hm.buckets
            .filter(bucket => Array.isArray(bucket) && bucket.length >= 4)
            .map(bucket => ({
                le: parseFloat(bucket[2]), // end as upper bound
                value: parseFloat(bucket[3]) // count
            }))
            .sort((a, b) => a.le - b.le);
    }
    
    if (metric.buckets && metric.buckets.length > 0) {
        metric.count = hm.count;
        metric.sum = hm.sum;
        metric.labels = hm.labels;
        result.histograms.push(metric);
    }
}

// Process summary metrics
function processSummary(metricFamily, metric, result) {
    const summaries = metricFamily.metrics?.filter(m => m.quantiles) || [];
    if (summaries.length === 0) return;
    
    const sm = summaries[0];
    
    metric.quantiles = Object.entries(sm.quantiles)
        .map(([quantile, value]) => ({
            quantile: parseFloat(quantile),
            value: parseFloat(value)
        }))
        .sort((a, b) => a.quantile - b.quantile);
    
    if (metric.quantiles && metric.quantiles.length > 0) {
        metric.count = sm.count;
        metric.sum = sm.sum;
        metric.labels = sm.labels;
        result.summaries.push(metric);
    }
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
    const metricsGrid = document.querySelector('.metrics-grid');
    if (!metricsGrid) return;
    
    const fragment = document.createDocumentFragment();
    const existingCards = {};
    const metricStillExists = new Set();
    const orderedElements = [];
    
    // Store existing cards in the DOM
    const existingCardsInDOM = document.querySelectorAll('.metric-card');
    existingCardsInDOM.forEach(card => {
        existingCards[card.getAttribute('data-metric-name')] = card;
    });
    
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
        
        if (existingCards[name]) {
            // Reuse existing card
            card = existingCards[name];
            // Update value if needed
            updateMetricCardValue(card, metric);
        } else {
            // Create new card for new metrics
            card = createMetricCard(metric);
        }
        
        // Add to ordered elements array
        orderedElements.push(card);
    });
    
    // Remove cards for metrics that no longer exist
    for (const metricName in existingCards) {
        if (!metricStillExists.has(metricName)) {
            existingCards[metricName].remove();
        }
    }
    
    if (orderedElements.length === 0) {
        metricsGrid.innerHTML = '<div class="no-results">No metrics found</div>';
        return;
    }

    if (metricsGrid.children.length === 0) {
        orderedElements.forEach(elem => fragment.appendChild(elem));
        metricsGrid.appendChild(fragment);
        return;
    }

    // Check if the order or content has changed
    let needsUpdate = metricsGrid.children.length !== orderedElements.length;
    if (!needsUpdate) {
        for (let i = 0; i < orderedElements.length; i++) {
            if (metricsGrid.children[i] !== orderedElements[i]) {
                needsUpdate = true;
                break;
            }
        }
    }

    if (needsUpdate) {
        orderedElements.forEach(elem => fragment.appendChild(elem));
        metricsGrid.innerHTML = '';
        metricsGrid.appendChild(fragment);
    }
}

// Create a new metric card
function createMetricCard(metric) {
    // Create card container
    const card = document.createElement('div');
    card.className = `metric-card ${metric.type.toLowerCase() || 'unknown'}-card`;
    card.setAttribute('data-metric-type', metric.type.toLowerCase() || 'unknown');
    card.setAttribute('data-metric-name', metric.name);
    
    // Prevent browser default title tooltip
    card.setAttribute('title', '');
    
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
    
    // Create value display
    const valueElement = document.createElement('div');
    valueElement.className = 'metric-value';
    
    if (metric.values && metric.values.length > 0) {
        valueElement.textContent = formatMetricValue(metric.values[0].value, metric.name);
    }
    
    card.appendChild(valueElement);
    
    // Add tooltip with help text
    const tooltip = createTooltip(metric);
    card.appendChild(tooltip);
    
    // Add click handler to show tooltip
    card.addEventListener('click', (e) => {
        e.preventDefault();
        e.stopPropagation();
        
        // Close any other open tooltips
        document.querySelectorAll('.tooltip-active').forEach(el => {
            if (el !== card) el.classList.remove('tooltip-active');
        });
        
        // Toggle tooltip visibility
        card.classList.toggle('tooltip-active');
    });
    
    return card;
}

// Update metric card value with animation
function updateMetricCardValue(card, metric) {
    if (!metric.values || metric.values.length === 0) return;
    
    const valueDisplay = card.querySelector('.metric-value');
    const newValue = formatMetricValue(metric.values[0].value, metric.name);
    
    if (valueDisplay.textContent !== newValue) {
        valueDisplay.textContent = newValue;
        // Add flash animation
        valueDisplay.classList.remove('value-updated');
        setTimeout(() => valueDisplay.classList.add('value-updated'), 10);
    }
    
    // Update tooltip
    const tooltip = card.querySelector('.tooltip');
    if (tooltip) {
        const helpElement = tooltip.querySelector('p');
        if (helpElement && metric.help) {
            helpElement.textContent = metric.help;
        }
    }
}

// Create tooltip element
function createTooltip(metric) {
    // Create tooltip element
    const tooltip = document.createElement('div');
    tooltip.className = 'tooltip';
    
    // Include full metric name at the top with formatting
    const nameElement = document.createElement('strong');
    nameElement.textContent = metric.name;
    tooltip.appendChild(nameElement);
    
    // Add help text if available
    if (metric.help) {
        const helpElement = document.createElement('p');
        helpElement.textContent = metric.help;
        tooltip.appendChild(helpElement);
    }
    
    return tooltip;
}

// Format metric values for display with appropriate units
function formatMetricValue(value, metricName) {
    if (typeof value !== 'number') return value.toString();
    
    // Convert bytes to MB if applicable
    if (metricName && isLikelyBytes(metricName)) {
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
    const typeSelect = document.getElementById('metricsType');
    const searchInput = document.getElementById('metricsSearch');
    
    if (typeSelect) {
        typeSelect.addEventListener('change', filterMetrics);
    }
    
    if (searchInput) {
        // Add fuzzy search functionality
        searchInput.addEventListener('keydown', () => {
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
        const clearSearchBtn = document.getElementById('clearMetricsSearch');
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
    const typeSelect = document.getElementById('metricsType');
    
    if (!typeSelect) return;
    
    const filterType = typeSelect.value.toLowerCase();
    
    // If "all" is selected, show everything
    if (filterType === 'all') {
        document.querySelectorAll('.metric-card, .chart-wrapper').forEach(el => {
            el.style.display = 'block';
        });
        
        // Make sure chart sections are visible
        document.querySelectorAll('.chart-section').forEach(section => {
            section.style.display = 'block';
        });
        
        return;
    }
    
    // Filter metric cards
    document.querySelectorAll('.metric-card').forEach(card => {
        const cardType = card.getAttribute('data-metric-type');
        card.style.display = cardType === filterType ? 'block' : 'none';
    });
    
    // Filter chart wrappers based on section type
    document.querySelectorAll('.chart-section').forEach(section => {
        const isHistogram = section.querySelector('h3')?.textContent === 'Histograms';
        const isSummary = section.querySelector('h3')?.textContent === 'Summaries';
        
        // Show/hide entire sections based on filter
        if ((isHistogram && filterType === 'histogram') || 
            (isSummary && filterType === 'summary')) {
            section.style.display = 'block';
        } else {
            section.style.display = 'none';
        }
    });
}

// Render chart wrappers for histogram and summary metrics
function renderGraphMetrics(histograms, summaries) {
    const chartsContainer = document.querySelector('.charts-container');
    if (!chartsContainer) return;
    
    // Create or find sections and render content
    if (histograms.length > 0) {
        renderChartSection(chartsContainer, 'Histograms', histograms, createHistogramChart, updateHistogramChart);
    }
    
    if (summaries.length > 0) {
        renderChartSection(chartsContainer, 'Summaries', summaries, createSummaryChart, updateSummaryChart);
    }
    
    // Remove empty sections
    document.querySelectorAll('.chart-section').forEach(section => {
        const grid = section.querySelector('.chart-grid');
        if (!grid || grid.children.length === 0) {
            section.remove();
        }
    });
}

// Helper function to render a section of charts
function renderChartSection(container, title, metrics, createChartFn, updateChartFn) {
    // Find or create section
    let section = null;
    document.querySelectorAll('.chart-section').forEach(s => {
        if (s.querySelector('h3')?.textContent === title) {
            section = s;
        }
    });
    
    if (!section) {
        section = document.createElement('div');
        section.className = 'chart-section';
        section.innerHTML = `<h3>${title}</h3>`;
        
        const grid = document.createElement('div');
        grid.className = 'chart-grid';
        section.appendChild(grid);
        
        container.appendChild(section);
    }
    
    const grid = section.querySelector('.chart-grid');
    
    // Store existing chart wrappers
    const existingCharts = {};
    grid.querySelectorAll('.chart-wrapper').forEach(wrapper => {
        existingCharts[wrapper.getAttribute('data-metric-name')] = wrapper;
    });
    
    // Keep track of metrics that still exist
    const metricsStillExist = new Set();
    
    // Create document fragment for efficient DOM manipulation
    const fragment = document.createDocumentFragment();
    const orderedWrappers = [];
    
    // Process each metric
    metrics.forEach(metric => {
        let wrapper;
        metricsStillExist.add(metric.name);
        
        if (existingCharts[metric.name]) {
            // Reuse existing wrapper
            wrapper = existingCharts[metric.name];
            
            // Update chart data
            const canvas = wrapper.querySelector('canvas');
            const chartInstance = Chart.getChart(canvas);
            
            if (chartInstance) {
                updateChartFn(chartInstance, metric);
            } else {
                // If chart instance is lost, recreate it
                createChartFn(canvas, metric);
            }
            
            // Update tooltip
            const tooltip = wrapper.querySelector('.tooltip');
            if (tooltip) {
                const helpElement = tooltip.querySelector('p');
                if (helpElement && metric.help) {
                    helpElement.textContent = metric.help;
                }
            }
        } else {
            // Create new wrapper and chart
            wrapper = document.createElement('div');
            wrapper.className = 'chart-wrapper';
            wrapper.setAttribute('data-metric-name', metric.name);
            wrapper.setAttribute('title', ''); // Prevent browser tooltip
            
            const canvas = document.createElement('canvas');
            wrapper.appendChild(canvas);
            
            // Add tooltip
            const tooltip = createChartTooltip(metric);
            wrapper.appendChild(tooltip);
            
            // Create chart after DOM insertion
            createChartFn(canvas, metric);
            
            // Add click handler to show tooltip
            wrapper.addEventListener('click', (e) => {
                e.preventDefault();
                e.stopPropagation();
                
                // Close any other open tooltips
                document.querySelectorAll('.tooltip-active').forEach(el => {
                    if (el !== wrapper) el.classList.remove('tooltip-active');
                });
                
                // Toggle tooltip visibility
                wrapper.classList.toggle('tooltip-active');
            });
        }
        
        orderedWrappers.push(wrapper);
    });
    
    if (orderedWrappers.length === 0) {
        return;
    }

    // Check if the grid needs updating
    let needsUpdate = grid.children.length !== orderedWrappers.length;
    if (!needsUpdate) {
        for (let i = 0; i < orderedWrappers.length; i++) {
            if (grid.children[i] !== orderedWrappers[i]) {
                needsUpdate = true;
                break;
            }
        }
    }

    if (needsUpdate) {
        orderedWrappers.forEach(wrapper => fragment.appendChild(wrapper));
        grid.innerHTML = '';
        grid.appendChild(fragment);
    }

    // Remove wrappers for metrics that no longer exist
    Object.keys(existingCharts).forEach(name => {
        if (!metricsStillExist.has(name)) {
            existingCharts[name].remove();
        }
    });
}

// Create tooltip for chart
function createChartTooltip(metric) {
    const tooltip = document.createElement('div');
    tooltip.className = 'tooltip';
    
    // Include full metric name at the top with formatting
    const nameElement = document.createElement('strong');
    nameElement.textContent = metric.name;
    tooltip.appendChild(nameElement);
    
    // Add help text if available
    if (metric.help) {
        const helpElement = document.createElement('p');
        helpElement.textContent = metric.help;
        tooltip.appendChild(helpElement);
    }
    
    // Add statistics for histograms and summaries
    if (metric.type === 'HISTOGRAM' && metric.count) {
        const statsElement = document.createElement('p');
        statsElement.innerHTML = `<b>Count:</b> ${formatMetricValue(metric.count, 'count')}`;
        tooltip.appendChild(statsElement);
        
        if (metric.sum) {
            const sumElement = document.createElement('p');
            sumElement.innerHTML = `<b>Sum:</b> ${formatMetricValue(metric.sum, 'sum')}`;
            tooltip.appendChild(sumElement);
        }
    } else if (metric.type === 'SUMMARY' && metric.count) {
        const statsElement = document.createElement('p');
        statsElement.innerHTML = `<b>Count:</b> ${formatMetricValue(metric.count, 'count')}`;
        tooltip.appendChild(statsElement);
        
        if (metric.sum) {
            const sumElement = document.createElement('p');
            sumElement.innerHTML = `<b>Sum:</b> ${formatMetricValue(metric.sum, 'sum')}`;
            tooltip.appendChild(sumElement);
        }
    }
    
    return tooltip;
}

// Create a histogram chart using Chart.js
function createHistogramChart(canvas, metric) {
    const buckets = metric.buckets || [];
    if (buckets.length === 0) return null;
    
    // Convert to non-cumulative data for display
    const { labels, values } = convertBucketsToNonCumulative(buckets);
    
    return new Chart(canvas, {
        type: 'bar',
        data: {
            labels: labels,
            datasets: [{
                label: formatMetricTitle(metric.name),
                data: values,
                backgroundColor: 'rgba(66, 133, 244, 0.5)', // Google Blue
                borderColor: '#4285F4',
                borderWidth: 1
            }]
        },
        options: {
            responsive: true,
            maintainAspectRatio: false,
            plugins: {
                title: {
                    display: true,
                    text: formatMetricTitle(metric.name),
                    font: {
                        size: 14,
                        weight: 'bold'
                    }
                },
                tooltip: {
                    callbacks: {
                        title: function(context) {
                            return `Bucket ${context[0].label}`;
                        },
                        label: function(context) {
                            return `Count: ${context.raw}`;
                        },
                        afterLabel: function(context) {
                            return `Cumulative: ${buckets[context.dataIndex].value}`;
                        }
                    }
                },
                legend: {
                    display: false
                }
            },
            scales: {
                y: {
                    beginAtZero: true,
                    title: {
                        display: true,
                        text: 'Frequency'
                    }
                },
                x: {
                    title: {
                        display: true,
                        text: 'Bucket Upper Bound'
                    }
                }
            },
            // Add event handler to prevent clicks on chart from closing tooltip
            onClick: function(e) {
                e.native.stopPropagation();
            }
        }
    });
}

// Convert histogram buckets to non-cumulative display data
function convertBucketsToNonCumulative(buckets) {
    const labels = [];
    const values = [];
    
    for (let i = 0; i < buckets.length; i++) {
        const bucketLabel = buckets[i].le === Infinity ? '+Inf' : buckets[i].le.toString();
        labels.push(bucketLabel);
        
        if (i === 0) {
            values.push(buckets[i].value);
        } else {
            // Convert cumulative to incremental
            const incrementalValue = buckets[i].value - buckets[i-1].value;
            values.push(incrementalValue >= 0 ? incrementalValue : 0); // Ensure non-negative
        }
    }
    
    return { labels, values };
}

// Update an existing histogram chart with new data
function updateHistogramChart(chart, metric) {
    const buckets = metric.buckets || [];
    if (buckets.length === 0) return;
    
    // Convert to non-cumulative data for display
    const { labels, values } = convertBucketsToNonCumulative(buckets);
    
    // Update chart data
    chart.data.labels = labels;
    chart.data.datasets[0].data = values;
    chart.options.plugins.title.text = formatMetricTitle(metric.name);
    chart.data.datasets[0].label = formatMetricTitle(metric.name);
    
    chart.update('none'); // Use 'none' animation mode for performance
}

// Create a summary chart using Chart.js
function createSummaryChart(canvas, metric) {
    const quantiles = metric.quantiles || [];
    if (quantiles.length === 0) return null;
    
    // Process quantiles for display
    const labels = quantiles.map(q => `p${Math.round(q.quantile * 100)}`);
    const values = quantiles.map(q => q.value);
    
    return new Chart(canvas, {
        type: 'line',
        data: {
            labels: labels,
            datasets: [{
                label: formatMetricTitle(metric.name),
                data: values,
                backgroundColor: 'rgba(234, 67, 53, 0.2)', // Google Red
                borderColor: '#EA4335',
                borderWidth: 2,
                pointBackgroundColor: '#EA4335',
                pointRadius: 5,
                pointHoverRadius: 7,
                tension: 0.1
            }]
        },
        options: {
            responsive: true,
            maintainAspectRatio: false,
            plugins: {
                title: {
                    display: true,
                    text: formatMetricTitle(metric.name),
                    font: {
                        size: 14,
                        weight: 'bold'
                    }
                },
                tooltip: {
                    callbacks: {
                        title: function(context) {
                            const quantileIndex = context[0].dataIndex;
                            return `Quantile: ${quantiles[quantileIndex].quantile}`;
                        },
                        label: function(context) {
                            return `Value: ${context.raw}`;
                        },
                        afterLabel: function(context) {
                            if (metric.count && metric.sum) {
                                return [
                                    `Count: ${metric.count}`,
                                    `Sum: ${metric.sum}`
                                ];
                            }
                            return null;
                        }
                    }
                },
                legend: {
                    display: false
                }
            },
            scales: {
                y: {
                    beginAtZero: false,
                    title: {
                        display: true,
                        text: 'Value'
                    }
                },
                x: {
                    title: {
                        display: true,
                        text: 'Percentile'
                    }
                }
            },
            // Add event handler to prevent clicks on chart from closing tooltip
            onClick: function(e) {
                e.native.stopPropagation();
            }
        }
    });
}

// Update an existing summary chart with new data
function updateSummaryChart(chart, metric) {
    const quantiles = metric.quantiles || [];
    if (quantiles.length === 0) return;
    
    // Process quantiles for display
    const labels = quantiles.map(q => `p${Math.round(q.quantile * 100)}`);
    const values = quantiles.map(q => q.value);
    
    // Update chart data
    chart.data.labels = labels;
    chart.data.datasets[0].data = values;
    chart.options.plugins.title.text = formatMetricTitle(metric.name);
    chart.data.datasets[0].label = formatMetricTitle(metric.name);
    
    chart.update('none'); // Use 'none' animation mode for performance
}

// Format metric title for display
function formatMetricTitle(name) {
    return name
        .replace(/_/g, ' ')  // Replace underscores with spaces
        .replace(/([a-z])([A-Z])/g, '$1 $2')  // Add space before capital letters
        .replace(/\b\w/g, c => c.toUpperCase());  // Capitalize first letter of each word
} 