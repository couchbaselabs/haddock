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

// Function to update layout based on window width
function updateSidebarLayout() {
    const sidebar = document.querySelector('.sidebar');
    const mainWrapper = document.querySelector('.main-wrapper');
    const sidebarToggle = document.getElementById('sidebarToggle');

    if (!sidebar || !mainWrapper || !sidebarToggle) return; // Ensure elements exist

    const isMobile = window.innerWidth <= 800;
    const isSidebarCollapsed = localStorage.getItem('sidebarCollapsed') === 'true';
    
    // Temporarily disable transitions during width-based layout changes
    sidebar.style.transition = "none";
    mainWrapper.style.transition = "none";
    
    // Force a reflow to apply the transition: none immediately
    sidebar.offsetHeight;
    
    if (isMobile) {
        // Mobile view: Remove collapsed class for bottom bar layout
        sidebar.classList.remove('collapsed');
        mainWrapper.classList.remove('expanded');
        sidebarToggle.style.display = 'none';
    } else {
        // Desktop view: Apply stored state
        sidebarToggle.style.display = 'flex';
        if (isSidebarCollapsed) {
            sidebar.classList.add('collapsed');
            mainWrapper.classList.add('expanded');
        } else {
            sidebar.classList.remove('collapsed');
            mainWrapper.classList.remove('expanded');
        }
    }
    
    // Re-enable transitions after a brief delay
    setTimeout(() => {
        sidebar.style.transition = "";
        mainWrapper.style.transition = "";
    }, 50);
}

// Initialize sidebar toggle functionality
function initSidebarToggle() {
    const sidebarToggle = document.getElementById('sidebarToggle');
    const sidebar = document.querySelector('.sidebar');
    const mainWrapper = document.querySelector('.main-wrapper');
    
    if (!sidebarToggle || !sidebar || !mainWrapper) return; // Exit if elements not found

    // Click handler for the toggle button (only relevant for desktop)
    sidebarToggle.addEventListener('click', () => {
        // Only toggle if in desktop view
        if (window.innerWidth > 800) { 
            const isCollapsed = sidebar.classList.toggle('collapsed');
            mainWrapper.classList.toggle('expanded', isCollapsed);

            // Store the collapsed state in localStorage
            localStorage.setItem('sidebarCollapsed', isCollapsed ? 'true' : 'false');
        }
    });

    // Set initial layout based on current width
    updateSidebarLayout(); 
    
    // Update layout on window resize
    window.addEventListener('resize', updateSidebarLayout);
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
    
    // Handle events page initialization
    if (pageId === 'events') {
        // Add any specific initialization for events here
        return;
    }
    
    // Handle logs page initialization
    if (pageId === 'logs') {
        // Add any specific initialization for logs here
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