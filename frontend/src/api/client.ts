import axios, { type InternalAxiosRequestConfig } from 'axios';

// In Docker/production: VITE_API_URL is empty → relative URLs → nginx proxies to BpmnGo
// In dev: VITE_API_URL=http://localhost:8080 OR Vite's proxy handles /engine-rest etc.
const BASE_URL = import.meta.env.VITE_API_URL ?? '';

export const apiClient = axios.create({
  baseURL: BASE_URL,
  headers: { 'Content-Type': 'application/json' },
});

type RetryableRequest = InternalAxiosRequestConfig & { _retry?: boolean };
type TokenPair = { access_token: string; refresh_token: string };
let pendingRefresh: Promise<string> | undefined;

function storedToken(key: string): string | null {
  const value = localStorage.getItem(key);
  return value && value !== 'undefined' && value !== 'null' ? value : null;
}

function isAuthRequest(url = ''): boolean {
  return /\/auth\/(login|refresh|logout)(?:[?#]|$)/.test(url);
}

function expiresSoon(token: string): boolean {
  try {
    const payload = token.split('.')[1].replace(/-/g, '+').replace(/_/g, '/');
    const { exp } = JSON.parse(atob(payload));
    return typeof exp === 'number' && exp * 1000 <= Date.now() + 30_000;
  } catch { return false; }
}

function expireSession() {
  localStorage.removeItem('accessToken');
  localStorage.removeItem('refreshToken');
  if (window.location.pathname !== '/login') window.location.assign('/login');
}

function refreshAccessToken(): Promise<string> {
  if (pendingRefresh) return pendingRefresh;
  const previousAccess = localStorage.getItem('accessToken');
  const previousRefresh = localStorage.getItem('refreshToken');
  const sameSession = () => localStorage.getItem('accessToken') === previousAccess && localStorage.getItem('refreshToken') === previousRefresh;
  pendingRefresh = (async () => {
    try {
      // The backend uses snake_case; an omitted token also allows its HTTP-only
      // refresh cookie to recover sessions affected by the old client bug.
      const refresh = storedToken('refreshToken');
      const { data } = await axios.post<TokenPair>(`${BASE_URL}/auth/refresh`,
        refresh ? { refresh_token: refresh } : {}, { withCredentials: true, timeout: 15_000 });
      if (!data.access_token || !data.refresh_token || typeof data.access_token !== 'string' || typeof data.refresh_token !== 'string') {
        throw new Error('Invalid token refresh response');
      }
      // A logout or a new login while refresh is in flight must take precedence.
      if (!sameSession()) throw new axios.CanceledError('Session changed during token refresh');
      localStorage.setItem('accessToken', data.access_token);
      localStorage.setItem('refreshToken', data.refresh_token);
      return data.access_token;
    } catch (error) {
      const status = axios.isAxiosError(error) ? error.response?.status : undefined;
      // Network errors and temporary server outages do not invalidate a session.
      if (sameSession() && (status === 400 || status === 401)) expireSession();
      throw error;
    }
  })().finally(() => { pendingRefresh = undefined; });
  return pendingRefresh;
}

apiClient.interceptors.request.use(async (config: RetryableRequest) => {
  let token = storedToken('accessToken');
  if (!isAuthRequest(config.url) && !config._retry && ((token && expiresSoon(token)) || (!token && storedToken('refreshToken')))) {
    token = await refreshAccessToken();
  }
  if (token) config.headers.Authorization = `Bearer ${token}`;
  else delete config.headers.Authorization;
  return config;
});

// Refresh token on 401
apiClient.interceptors.response.use(
  (res) => res,
  async (error) => {
    const original = error.config as RetryableRequest | undefined;
    if (error.response?.status === 401 && original && !isAuthRequest(original.url) && !original._retry) {
      original._retry = true;
      const current = storedToken('accessToken');
      // A late 401 may belong to a request sent before another request refreshed.
      const token = current && original.headers.Authorization !== `Bearer ${current}`
        ? current : await refreshAccessToken();
      original.headers.Authorization = `Bearer ${token}`;
      return apiClient(original);
    }
    return Promise.reject(error);
  }
);
