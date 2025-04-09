// core.js - Common global variables and utility functions
export const socket = new WebSocket("ws://" + window.location.host + "/ws");

// Timing constants
export const LOG_BATCH_INTERVAL = 500;
export const EVENT_BATCH_INTERVAL = 10;
export const SEARCH_DEBOUNCE_DELAY = 300; // Delay in ms for search debounce

// Session ID generator utility
export function generateSessionId() {
    return Date.now().toString() + window.crypto.getRandomValues(new Uint32Array(1))[0];
}

// Text highlighting utility
export function highlightMatches(text, matches) {
    if (!matches || !text) return text;
    
    // Sort matches by indices from end to beginning
    const sortedMatches = [...matches].sort((a, b) => b[0] - a[0]);
    
    let result = text;
    
    // Process each match from end to beginning
    for (const [start, end] of sortedMatches) {
        const matchedText = result.substring(start, end + 1);
        const highlighted = `<span class="match-highlight">${matchedText}</span>`;
        result = result.substring(0, start) + highlighted + result.substring(end + 1);
    }
    
    return result;
} 