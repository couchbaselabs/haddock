// Handle SPA navigation in the dashboard
document.addEventListener('DOMContentLoaded', () => {
    // Initialize navigation and sidebar first
    initNavigation();
    initSidebarToggle();
    
    // Get the initial page from hash or default to dashboard
    const hash = window.location.hash.substring(1);
    const initialPage = hash || 'dashboard';
    
    // Update active state before showing the page to prevent flash
    updateActiveNavItem(initialPage);
    
    // Show the correct page
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
            
            // Update active state first
            updateActiveNavItem(page);
            
            // Update hash without triggering another hashchange event
            history.replaceState(null, null, `#${page}`);
            
            // Show the page
            showPage(page);
        });
    });
}

// Initialize sidebar toggle functionality
function initSidebarToggle() {
    const sidebarToggle = document.getElementById('sidebarToggle');
    const sidebar = document.querySelector('.sidebar');
    const mainWrapper = document.querySelector('.main-wrapper');
    
    // Check for stored sidebar state
    const isSidebarCollapsed = localStorage.getItem('sidebarCollapsed') === 'true';
    
    // Apply initial state if needed
    if (isSidebarCollapsed) {
        sidebar.classList.add('collapsed');
        mainWrapper.classList.add('expanded');
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
    // Hide all pages first
    document.querySelectorAll('.page-content').forEach(page => {
        page.classList.add('hidden');
    });
    
    // Find and show the requested page
    const targetPage = document.getElementById(`${pageId}-page`);
    if (!targetPage) {
        return;
    }
    
    targetPage.classList.remove('hidden');
    
    // Handle metrics page initialization
    if (pageId === 'metrics') {
        if (typeof startMetricsRefresh === 'function') {
            startMetricsRefresh();
        }
        return;
    }
    
    // Stop metrics refresh for non-metrics pages
    if (typeof stopMetricsRefresh === 'function') {
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