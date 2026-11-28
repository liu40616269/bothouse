import { api } from './client';

export interface RelayHealthStatus {
  enabled: boolean;
  target?: string;
  state: 'disabled' | 'unknown' | 'ok' | 'auth_expired' | 'unreachable';
  message?: string;
  projects?: string[];
  checked_at?: string;
  since?: string;
  webrelay_available?: boolean;
}

export interface RelayAuthSnapshot {
  active: boolean;
  state: 'idle' | 'running' | 'succeeded' | 'failed' | 'canceled';
  target?: string;
  output?: string;
  urls?: string[];
  error?: string;
  started_at?: string;
}

export const getRelayStatus = () => api.get<RelayHealthStatus>('/relay/status');
export const checkRelayNow = () => api.post<RelayHealthStatus>('/relay/check');

export const startRelayAuth = () => api.post<RelayAuthSnapshot>('/relay/auth/start');
export const pollRelayAuth = () => api.get<RelayAuthSnapshot>('/relay/auth/poll');
export const sendRelayAuthInput = (input: string) => api.post<RelayAuthSnapshot>('/relay/auth/input', { input });
export const cancelRelayAuth = () => api.post<RelayAuthSnapshot>('/relay/auth/cancel');
export const openWebRelayApp = () => api.post<{ launched: boolean; app: string }>('/relay/auth/webrelay');
