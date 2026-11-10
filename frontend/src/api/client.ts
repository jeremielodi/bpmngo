import axios from 'axios';

// In Docker/production: VITE_API_URL is empty → relative URLs → nginx proxies to BpmnGo
// In dev: VITE_API_URL=http://localhost:8080 OR Vite's proxy handles /engine-rest etc.
const BASE_URL = import.meta.env.VITE_API_URL ?? '';

export const apiClient = axios.create({
  baseURL: BASE_URL,
  headers: { 'Content-Type': 'application/json' },
});

// Inject access token from localStorage
apiClient.interceptors.request.use((config) => {
  const token = localStorage.getItem('accessToken');
  if (token) config.headers.Authorization = `Bearer ${token}`;
  return config;
});

// Refresh token on 401
apiClient.interceptors.response.use(
  (res) => res,
  async (error) => {
    const original = error.config;
    if (error.response?.status === 401 && !original._retry) {
      original._retry = true;
      try {
        const refresh = localStorage.getItem('refreshToken');
        const res = await axios.post(`${BASE_URL || ''}/auth/refresh`, { refreshToken: refresh });
        const { accessToken, refreshToken } = res.data;
        localStorage.setItem('accessToken', accessToken);
        localStorage.setItem('refreshToken', refreshToken);
        original.headers.Authorization = `Bearer ${accessToken}`;
        return apiClient(original);
      } catch {
        localStorage.removeItem('accessToken');
        localStorage.removeItem('refreshToken');
        window.location.href = '/login';
      }
    }
    return Promise.reject(error);
  }
);

// Extracts the server's error message from a failed request: BpmnGo answers
// {"error": ...}, Camunda-style endpoints {"message": ...} or {"detail": ...}.
export function apiErrorMessage(err: unknown, fallback: string): string {
  const e = err as { response?: { data?: unknown }; message?: string };
  const data = e?.response?.data;
  if (typeof data === 'string' && data.trim()) return data;
  if (data && typeof data === 'object') {
    const d = data as Record<string, unknown>;
    for (const k of ['error', 'message', 'detail']) {
      if (typeof d[k] === 'string' && d[k]) return d[k] as string;
    }
  }
  return e?.message || fallback;
}
