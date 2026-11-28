import { api } from './client';

export interface RelayHealthStatus {
  enabled: boolean;
  target?: string;
  state: 'disabled' | 'unknown' | 'ok' | 'auth_expired' | 'unreachable';
  message?: string;
  projects?: string[];
  checked_at?: string;
  since?: string;
}

export const getRelayStatus = () => api.get<RelayHealthStatus>('/relay/status');
export const checkRelayNow = () => api.post<RelayHealthStatus>('/relay/check');
