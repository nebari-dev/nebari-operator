// Configuration
const CONFIG = {
    apiBaseUrl: '/api/v1',
    keycloakUrl: window.location.origin.includes('localhost')
        ? 'http://localhost:8080'
        : 'https://keycloak.nebari.example.com',
    keycloakRealm: 'nebari',
    keycloakClientId: 'landing-page',
    storageKeys: {
        accessToken: 'nebari_access_token',
        refreshToken: 'nebari_refresh_token',
        pkceVerifier: 'nebari_pkce_verifier'
    }
};

// DOM Elements
const elements = {
    signInBtn: document.getElementById('sign-in-btn'),
    signOutBtn: document.getElementById('sign-out-btn'),
    userInfo: document.getElementById('user-info'),
    userName: document.getElementById('user-name'),
    userGroups: document.getElementById('user-groups'),
    publicSection: document.getElementById('public-section'),
    authenticatedSection: document.getElementById('authenticated-section'),
    privateSection: document.getElementById('private-section'),
    errorSection: document.getElementById('error-section'),
    errorText: document.getElementById('error-text'),
    retryBtn: document.getElementById('retry-btn'),
    publicServices: document.getElementById('public-services'),
    authenticatedServices: document.getElementById('authenticated-services'),
    privateServices: document.getElementById('private-services')
};

// State
let currentUser = null;

// PKCE helper functions
function generateCodeVerifier() {
    const array = new Uint8Array(32);
    crypto.getRandomValues(array);
    return base64URLEncode(array);
}

async function generateCodeChallenge(verifier) {
    const encoder = new TextEncoder();
    const data = encoder.encode(verifier);
    const hash = await crypto.subtle.digest('SHA-256', data);
    return base64URLEncode(new Uint8Array(hash));
}

function base64URLEncode(buffer) {
    const base64 = btoa(String.fromCharCode.apply(null, buffer));
    return base64.replace(/\+/g, '-').replace(/\//g, '_').replace(/=/g, '');
}

// Authentication
async function initiateLogin() {
    try {
        const verifier = generateCodeVerifier();
        const challenge = await generateCodeChallenge(verifier);

        // Store verifier for later use
        sessionStorage.setItem(CONFIG.storageKeys.pkceVerifier, verifier);

        const params = new URLSearchParams({
            client_id: CONFIG.keycloakClientId,
            redirect_uri: window.location.origin,
            response_type: 'code',
            scope: 'openid profile email',
            code_challenge: challenge,
            code_challenge_method: 'S256'
        });

        const keycloakAuthUrl = `${CONFIG.keycloakUrl}/realms/${CONFIG.keycloakRealm}/protocol/openid-connect/auth`;
        window.location.href = `${keycloakAuthUrl}?${params}`;
    } catch (error) {
        console.error('Login initiation failed:', error);
        showError('Failed to initiate login');
    }
}

async function handleAuthCallback() {
    const urlParams = new URLSearchParams(window.location.search);
    const code = urlParams.get('code');

    if (!code) return false;

    try {
        const verifier = sessionStorage.getItem(CONFIG.storageKeys.pkceVerifier);
        if (!verifier) {
            throw new Error('PKCE verifier not found');
        }

        const params = new URLSearchParams({
            grant_type: 'authorization_code',
            client_id: CONFIG.keycloakClientId,
            code: code,
            redirect_uri: window.location.origin,
            code_verifier: verifier
        });

        const tokenUrl = `${CONFIG.keycloakUrl}/realms/${CONFIG.keycloakRealm}/protocol/openid-connect/token`;
        const response = await fetch(tokenUrl, {
            method: 'POST',
            headers: {
                'Content-Type': 'application/x-www-form-urlencoded'
            },
            body: params
        });

        if (!response.ok) {
            throw new Error('Token exchange failed');
        }

        const tokens = await response.json();

        // Store tokens
        sessionStorage.setItem(CONFIG.storageKeys.accessToken, tokens.access_token);
        if (tokens.refresh_token) {
            sessionStorage.setItem(CONFIG.storageKeys.refreshToken, tokens.refresh_token);
        }

        // Clean up
        sessionStorage.removeItem(CONFIG.storageKeys.pkceVerifier);

        // Remove code from URL
        window.history.replaceState({}, document.title, window.location.pathname);

        return true;
    } catch (error) {
        console.error('Auth callback failed:', error);
        showError('Authentication failed');
        return false;
    }
}

function logout() {
    // Clear tokens
    sessionStorage.removeItem(CONFIG.storageKeys.accessToken);
    sessionStorage.removeItem(CONFIG.storageKeys.refreshToken);
    sessionStorage.removeItem(CONFIG.storageKeys.pkceVerifier);

    // Reset state
    currentUser = null;
    updateAuthUI();

    // Reload to show public services only
    loadServices();
}

// API calls
async function fetchServices() {
    const token = sessionStorage.getItem(CONFIG.storageKeys.accessToken);

    const headers = {};
    if (token) {
        headers['Authorization'] = `Bearer ${token}`;
    }

    const response = await fetch(`${CONFIG.apiBaseUrl}/services`, { headers });

    if (!response.ok) {
        throw new Error(`API request failed: ${response.statusText}`);
    }

    return response.json();
}

// Parse JWT to get user info
function parseJWT(token) {
    try {
        const base64Url = token.split('.')[1];
        const base64 = base64Url.replace(/-/g, '+').replace(/_/g, '/');
        const jsonPayload = decodeURIComponent(atob(base64).split('').map(c => {
            return '%' + ('00' + c.charCodeAt(0).toString(16)).slice(-2);
        }).join(''));

        return JSON.parse(jsonPayload);
    } catch (error) {
        console.error('JWT parsing failed:', error);
        return null;
    }
}

// UI updates
function updateAuthUI() {
    const token = sessionStorage.getItem(CONFIG.storageKeys.accessToken);
    const isAuthenticated = !!token;

    if (isAuthenticated) {
        const claims = parseJWT(token);
        if (claims) {
            currentUser = {
                name: claims.name || claims.preferred_username || 'User',
                groups: claims.groups || []
            };

            elements.userName.textContent = currentUser.name;
            elements.userGroups.textContent = currentUser.groups.length
                ? `Groups: ${currentUser.groups.join(', ')}`
                : 'No groups';
            elements.userInfo.classList.remove('hidden');
            elements.signInBtn.classList.add('hidden');
        }
    } else {
        elements.userInfo.classList.add('hidden');
        elements.signInBtn.classList.remove('hidden');
    }
}

function renderServices(data) {
    // Show/hide sections based on authentication and data
    const hasAuth = !!sessionStorage.getItem(CONFIG.storageKeys.accessToken);

    // Always show public section
    elements.publicSection.classList.remove('hidden');
    renderServiceList(data.services.public, elements.publicServices);

    // Show authenticated section if logged in and has services
    if (hasAuth && data.services.authenticated.length > 0) {
        elements.authenticatedSection.classList.remove('hidden');
        renderServiceList(data.services.authenticated, elements.authenticatedServices);
    } else {
        elements.authenticatedSection.classList.add('hidden');
    }

    // Show private section if logged in and has services
    if (hasAuth && data.services.private.length > 0) {
        elements.privateSection.classList.remove('hidden');
        renderServiceList(data.services.private, elements.privateServices);
    } else {
        elements.privateSection.classList.add('hidden');
    }
}

function renderServiceList(services, container) {
    if (services.length === 0) {
        container.innerHTML = '<p class="loading">No services available</p>';
        return;
    }

    container.innerHTML = services.map(service => `
        <div class="service-card" onclick="window.open('${service.url}', '_blank')">
            <div class="service-header">
                <div class="service-icon">${getServiceIcon(service.icon)}</div>
                <h3 class="service-title">${service.displayName || service.name}</h3>
            </div>
            ${service.description ? `<p class="service-description">${service.description}</p>` : ''}
            <div class="service-footer">
                <span class="service-category">${service.category || 'General'}</span>
                ${renderHealthStatus(service.health)}
            </div>
        </div>
    `).join('');
}

function getServiceIcon(icon) {
    const iconMap = {
        jupyter: '📓',
        grafana: '📊',
        prometheus: '📈',
        keycloak: '🔐',
        argocd: '🔄',
        kubernetes: '☸️',
        dashboard: '🏠',
        database: '💾',
        api: '🔌'
    };

    return iconMap[icon] || '🚀';
}

function renderHealthStatus(health) {
    if (!health) return '';

    const statusClass = `health-${health.status}`;
    return `<span class="service-health ${statusClass}">${health.status}</span>`;
}

function showError(message) {
    elements.errorText.textContent = message;
    elements.errorSection.classList.remove('hidden');
    elements.publicSection.classList.add('hidden');
    elements.authenticatedSection.classList.add('hidden');
    elements.privateSection.classList.add('hidden');
}

function hideError() {
    elements.errorSection.classList.add('hidden');
}

// Main functions
async function loadServices() {
    try {
        hideError();
        const data = await fetchServices();
        renderServices(data);
    } catch (error) {
        console.error('Failed to load services:', error);
        showError('Failed to load services. Please try again.');
    }
}

async function init() {
    // Check for auth callback
    const authSuccess = await handleAuthCallback();

    // Update auth UI
    updateAuthUI();

    // Load services
    await loadServices();

    // Event listeners
    elements.signInBtn?.addEventListener('click', initiateLogin);
    elements.signOutBtn?.addEventListener('click', logout);
    elements.retryBtn?.addEventListener('click', loadServices);
}

// Start the app
document.addEventListener('DOMContentLoaded', init);
