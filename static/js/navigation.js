// Handle SPA navigation in the dashboard
document.addEventListener('DOMContentLoaded', () => {
    // Check URL hash on initial load to set the correct page
    const hash = window.location.hash.substring(1);
    
    // Only default to dashboard if no hash is present
    const initialPage = hash || 'dashboard';
    
    // Initialize navigation
    initNavigation();
    
    // Set up sidebar toggle
    initSidebarToggle();
    
    // Load correct page without flashing
    showPage(initialPage);
    
    // Listen for hash changes
    window.addEventListener('hashchange', () => {
        const hash = window.location.hash.substring(1) || 'dashboard';
        showPage(hash);
    });
});

// Initialize navigation elements
function initNavigation() {
    const navItems = document.querySelectorAll('.nav-item');
    
    navItems.forEach(item => {
        item.addEventListener('click', function(e) {
            const page = this.getAttribute('data-page');
            showPage(page);
            
            // Update hash without triggering another hashchange event
            history.replaceState(null, null, `#${page}`);
            
            // Update active state
            updateActiveNavItem(page);
        });
    });
}

// Initialize sidebar toggle functionality
function initSidebarToggle() {
    const sidebarToggle = document.getElementById('sidebarToggle');
    const sidebar = document.querySelector('.sidebar');
    const mainWrapper = document.querySelector('.main-wrapper');
    const toggleIcon = sidebarToggle.querySelector('.toggle-icon');
    
    // Check for stored sidebar state
    const isSidebarCollapsed = localStorage.getItem('sidebarCollapsed') === 'true';
    
    // Apply initial state if needed
    if (isSidebarCollapsed) {
        sidebar.classList.add('collapsed');
        mainWrapper.classList.add('expanded');
        // The rotation will be handled by CSS
    }
    
    // Add click handler
    sidebarToggle.addEventListener('click', () => {
        sidebar.classList.toggle('collapsed');
        mainWrapper.classList.toggle('expanded');
        
        // Store the collapsed state in localStorage
        localStorage.setItem('sidebarCollapsed', sidebar.classList.contains('collapsed') ? 'true' : 'false');
    });
}

// Show the selected page and hide others
function showPage(pageId) {
    document.querySelectorAll('.page-content').forEach(page => {
        page.classList.add('hidden');
    });
    
    // Find and show the requested page
    const targetPage = document.getElementById(`${pageId}-page`);
    if (targetPage) {
        targetPage.classList.remove('hidden');
    }
    
    // Update active state in navigation
    updateActiveNavItem(pageId);
    
    // Handle special pages that need initialization
    if (pageId === 'metrics' && typeof startMetricsRefresh === 'function') {
        startMetricsRefresh();
    } else if (pageId !== 'metrics' && typeof stopMetricsRefresh === 'function') {
        stopMetricsRefresh();
    }
}

// Update the active state in the navigation
function updateActiveNavItem(pageId) {
    const navItems = document.querySelectorAll('.nav-item');
    navItems.forEach(el => {
        if (el.getAttribute('data-page') === pageId) {
            el.classList.add('active');
        } else {
            el.classList.remove('active');
        }
    });
} 