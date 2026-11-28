import { useCallback, useEffect, useState } from 'react';
import { CheckCircle2, AlertTriangle, RefreshCw, ShieldAlert, Radio } from 'lucide-react';
import { Button } from '@/components/ui';
import { cn } from '@/lib/utils';
import { checkRelayNow, getRelayStatus, type RelayHealthStatus } from '@/api/relay';

function formatTime(iso?: string): string {
  if (!iso) return '';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

export function RelayStatusBanner() {
  const [status, setStatus] = useState<RelayHealthStatus | null>(null);
  const [checking, setChecking] = useState(false);

  const load = useCallback(async () => {
    try {
      const res = await getRelayStatus();
      setStatus(res);
    } catch {
      // Ignore transient load errors; avoid noisy banners when API is restarting.
    }
  }, []);

  useEffect(() => {
    load();
    const onRefresh = () => { load(); };
    window.addEventListener('cc:refresh', onRefresh);
    const timer = window.setInterval(load, 60_000);
    return () => {
      window.removeEventListener('cc:refresh', onRefresh);
      window.clearInterval(timer);
    };
  }, [load]);

  const handleCheck = async () => {
    setChecking(true);
    try {
      const res = await checkRelayNow();
      setStatus(res);
    } catch {
      // keep previous status
    } finally {
      setChecking(false);
    }
  };

  if (!status || !status.enabled || status.state === 'disabled') {
    return null;
  }

  const isOK = status.state === 'ok';
  const isExpired = status.state === 'auth_expired';
  const isUnreachable = status.state === 'unreachable';

  const toneClass = isOK
    ? 'bg-surface shadow-ui-1 border border-emerald-500/20'
    : isExpired
      ? 'bg-red-500/10 border border-red-500/30'
      : isUnreachable
        ? 'bg-amber-500/10 border border-amber-500/30'
        : 'bg-surface shadow-ui-1 border border-hairline';

  const iconClass = isOK
    ? 'bg-emerald-500/15 text-emerald-700 dark:text-emerald-400'
    : isExpired
      ? 'bg-red-500/15 text-red-700 dark:text-red-400'
      : isUnreachable
        ? 'bg-amber-500/15 text-amber-700 dark:text-amber-400'
        : 'bg-surface-sunken text-ink-mute';

  const title = isOK
    ? '跳板机已连接'
    : isExpired
      ? '跳板机登录态已过期，请重新认证'
      : isUnreachable
        ? '跳板机连接异常'
        : '跳板机状态检测中';

  const checkedTime = formatTime(status.checked_at);

  return (
    <div
      className={cn('flex flex-wrap items-center justify-between gap-3 rounded-card px-4 py-3 text-sm', toneClass)}
      data-testid="relay-status-banner"
    >
      <div className="flex min-w-0 items-center gap-3">
        <span className={cn('grid h-7 w-7 shrink-0 place-items-center rounded-full', iconClass)}>
          {isOK ? (
            <CheckCircle2 size={15} />
          ) : isExpired ? (
            <ShieldAlert size={15} />
          ) : isUnreachable ? (
            <AlertTriangle size={15} />
          ) : (
            <Radio size={15} />
          )}
        </span>
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5">
            <span className="font-semibold text-ink">{title}</span>
            {status.target && (
              <span className="truncate font-mono text-xs text-ink-mute">{status.target}</span>
            )}
          </div>
          <div className="mt-0.5 flex flex-wrap items-center gap-x-2 text-xs text-ink-soft">
            {status.message && <span>{status.message}</span>}
            {status.projects && status.projects.length > 0 && (
              <>
                <span aria-hidden>·</span>
                <span>远端角色：{status.projects.join(' / ')}</span>
              </>
            )}
            {checkedTime && (
              <>
                <span aria-hidden>·</span>
                <span className="tabular-nums text-ink-mute">上次检测 {checkedTime}</span>
              </>
            )}
          </div>
        </div>
      </div>

      <Button
        variant="secondary"
        size="sm"
        onClick={handleCheck}
        disabled={checking}
        className="shrink-0 gap-1.5"
      >
        <RefreshCw size={13} className={cn(checking && 'animate-spin motion-reduce:animate-none')} />
        {checking ? '检测中…' : '立即检测'}
      </Button>
    </div>
  );
}
