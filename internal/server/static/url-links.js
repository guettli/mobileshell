// Make URLs in output containers clickable
function makeUrlsClickable() {
    // URL regex pattern to match http, https, and common URL patterns
    const urlRegex = /(https?:\/\/[^\s<>"{}|\\^`\[\]]+)/gi;
    
    // Helper function to escape HTML
    function escapeHtml(text) {
        const div = document.createElement('div');
        div.textContent = text;
        return div.innerHTML;
    }
    
    // Get all output containers
    const containers = document.querySelectorAll('.output-container');
    
    containers.forEach(container => {
        // Skip if already processed
        if (container.dataset.urlsProcessed) return;
        
        // Get the text content
        const text = container.textContent;
        
        // Replace URLs with links, escaping HTML in the process
        const html = text.replace(urlRegex, (url) => {
            const escapedUrl = escapeHtml(url);
            return `<a href="${escapedUrl}" target="_blank" rel="noopener noreferrer" style="color: #0066cc; text-decoration: underline;">${escapedUrl}</a>`;
        });
        
        // Only update if we found URLs
        if (html !== text) {
            container.innerHTML = html;
        }
        container.dataset.urlsProcessed = 'true';
    });
}

// Auto-initialize when script loads
(function() {
    // Run on DOMContentLoaded
    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', makeUrlsClickable);
    } else {
        // DOM already loaded, run immediately
        makeUrlsClickable();
    }
    
    // Listen for HTMX after swap events to process dynamically loaded content
    if (document.body) {
        document.body.addEventListener('htmx:afterSwap', makeUrlsClickable);
    } else {
        // Body not ready yet, wait for DOMContentLoaded
        document.addEventListener('DOMContentLoaded', function() {
            document.body.addEventListener('htmx:afterSwap', makeUrlsClickable);
        });
    }
})();
