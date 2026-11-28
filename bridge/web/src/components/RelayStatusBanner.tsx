import { useCallback, useEffect, useRef, useState } from 'react';
import { CheckCircle2, AlertTriangle, RefreshCw, ShieldAlert, Radio, KeyRound, ExternalLink, Send, Fingerprint } from 'lucide-react';
import { Button, Modal } from '@/components/ui';
import { cn } from '@/lib/utils';
import {
  cancelRelayAuth,
  checkRelayNow,
  getRelayStatus,
  openWebRelayApp,
  pollRelayAuth,
  sendRelayAuthInput,
  startRelayAuth,
  type RelayAuthSnapshot,
  type RelayHealthStatus,
} from '@/api/relay';

function formatTime(iso?: string): string {
  if (!iso) return '';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

export function RelayStatusBanner() {
  const [status, setStatus] = useState<RelayHealthStatus | null>(null);
  const [checking, setChecking] = useState(false);
  const [waitingWebRelay, setWaitingWebRelay] = useState(false);
  const webRelayPollRef = useRef<number | null>(null);

  const [authOpen, setAuthOpen] = useState(false);
  const [authSnap, setAuthSnap] = useState<RelayAuthSnapshot | null>(null);
  const [authInput, setAuthInput] = useState('');
  const [sendingInput, setSendingInput] = useState(false);
  const [authError, setAuthError] = useState('');
  const outputRef = useRef<HTMLPreElement | null>(null);

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

  // Poll interactive auth session while modal is open and running.
  useEffect(() => {
    if (!authOpen) return;
    if (authSnap && authSnap.state !== 'running') return;
    const timer = window.setInterval(async () => {
      try {
        const snap = await pollRelayAuth();
        setAuthSnap(snap);
        if (snap.state === 'succeeded') {
          await load();
        }
      } catch {
        // ignore transient poll error
      }
    }, 1000);
    return () => window.clearInterval(timer);
  }, [authOpen, authSnap, load]);

  useEffect(() => {
    if (outputRef.current) {
      outputRef.current.scrollTop = outputRef.current.scrollHeight;
    }
  }, [authSnap?.output]);

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

  useEffect(() => {
    return () => {
      if (webRelayPollRef.current !== null) {
        window.clearInterval(webRelayPollRef.current);
      }
    };
  }, []);

  const handleOpenWebRelay = async () => {
    setWaitingWebRelay(true);
    try {
      await openWebRelayApp();
    } catch {
      setWaitingWebRelay(false);
      return;
    }
    if (webRelayPollRef.current !== null) {
      window.clearInterval(webRelayPollRef.current);
    }
    let attempts = 0;
    webRelayPollRef.current = window.setInterval(async () => {
      attempts += 1;
      try {
        const res = await checkRelayNow();
        setStatus(res);
        if (res.state === 'ok' || attempts >= 30) {
          if (webRelayPollRef.current !== null) {
            window.clearInterval(webRelayPollRef.current);
            webRelayPollRef.current = null;
          }
          setWaitingWebRelay(false);
        }
      } catch {
        if (attempts >= 30) {
          if (webRelayPollRef.current !== null) {
            window.clearInterval(webRelayPollRef.current);
            webRelayPollRef.current = null;
          }
          setWaitingWebRelay(false);
        }
      }
    }, 2000);
  };

  const handleStartAuth = async () => {
    setAuthOpen(true);
    setAuthError('');
    setAuthInput('');
    setAuthSnap({ active: true, state: 'running', target: status?.target, output: '正在连接跳板机…' });
    try {
      const snap = await startRelayAuth();
      setAuthSnap(snap);
      if (snap.state === 'succeeded') {
        await load();
      }
    } catch (e: any) {
      setAuthError(e?.message || '启动认证失败');
      setAuthSnap({ active: false, state: 'failed', target: status?.target, error: e?.message || '启动认证失败' });
    }
  };

  const handleSendInput = async (e?: React.FormEvent) => {
    if (e) e.preventDefault();
    if (!authSnap || authSnap.state !== 'running') return;
    setSendingInput(true);
    setAuthError('');
    try {
      const snap = await sendRelayAuthInput(authInput);
      setAuthInput('');
      setAuthSnap(snap);
    } catch (err: any) {
      setAuthError(err?.message || '发送失败');
    } finally {
      setSendingInput(false);
    }
  };

  const handleCloseAuth = async () => {
    if (authSnap?.state === 'running') {
      try {
        await cancelRelayAuth();
      } catch {
        // ignore
      }
    }
    setAuthOpen(false);
    setAuthInput('');
    setAuthError('');
    await load();
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
    <>
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

        <div className="flex shrink-0 flex-wrap items-center gap-2">
          {!isOK && status.webrelay_available && (
            <Button
              size="sm"
              onClick={handleOpenWebRelay}
              disabled={waitingWebRelay}
              className="gap-1.5"
            >
              <Fingerprint size={14} />
              {waitingWebRelay ? '等待指纹认证…' : 'WebRelay 指纹认证'}
            </Button>
          )}
          {!isOK && (
            <Button
              variant={status.webrelay_available ? 'secondary' : 'primary'}
              size="sm"
              onClick={handleStartAuth}
              className="gap-1.5"
            >
              <KeyRound size={13} />
              {status.webrelay_available ? '密码认证' : '重新认证'}
            </Button>
          )}
          <Button
            variant="secondary"
            size="sm"
            onClick={handleCheck}
            disabled={checking}
            className="gap-1.5"
          >
            <RefreshCw size={13} className={cn(checking && 'animate-spin motion-reduce:animate-none')} />
            {checking ? '检测中…' : '立即检测'}
          </Button>
        </div>
      </div>

      <Modal
        open={authOpen}
        onClose={handleCloseAuth}
        title="跳板机认证"
      >
        <div className="space-y-4">
          <div className="flex items-center justify-between text-xs text-ink-soft">
            <span>目标：<code className="font-mono text-ink">{authSnap?.target || status.target}</code></span>
            <span className={cn(
              'rounded-pill px-2 py-0.5 font-medium',
              authSnap?.state === 'succeeded'
                ? 'bg-emerald-500/15 text-emerald-700 dark:text-emerald-400'
                : authSnap?.state === 'failed'
                  ? 'bg-red-500/15 text-red-700 dark:text-red-400'
                  : 'bg-amber-500/15 text-amber-700 dark:text-amber-400',
            )}>
              {authSnap?.state === 'succeeded'
                ? '认证成功'
                : authSnap?.state === 'failed'
                  ? '认证失败'
                  : authSnap?.state === 'canceled'
                    ? '已取消'
                    : '等待交互认证…'}
            </span>
          </div>

          <pre
            ref={outputRef}
            className="max-h-60 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-surface-sunken p-3 font-mono text-xs leading-relaxed text-ink"
          >
            {authSnap?.output || '正在等待跳板机返回认证提示…'}
          </pre>

          {authSnap?.urls && authSnap.urls.length > 0 && (
            <div className="space-y-1.5 rounded-lg border border-hairline bg-surface p-2.5 text-xs">
              <div className="font-medium text-ink-soft">检测到认证链接（可点击打开完成认证）：</div>
              {authSnap.urls.map((u) => (
                <a
                  key={u}
                  href={u}
                  target="_blank"
                  rel="noreferrer"
                  className="flex items-center gap-1 break-all text-accent-text hover:underline"
                >
                  <ExternalLink size={12} className="shrink-0" />
                  <span>{u}</span>
                </a>
              ))}
            </div>
          )}

          {(authError || authSnap?.error) && (
            <div className="rounded-lg bg-red-500/10 px-3 py-2 text-xs text-red-700 dark:text-red-400">
              {authError || authSnap?.error}
            </div>
          )}

          {authSnap?.state === 'running' && (
            <form onSubmit={handleSendInput} className="flex items-center gap-2">
              <input
                type="password"
                autoFocus
                value={authInput}
                onChange={(e) => setAuthInput(e.target.value)}
                placeholder="输入密码 / OTP 验证码，或直接回车（不会保存或记录日志）"
                className="h-9 flex-1 rounded-lg border border-hairline bg-canvas px-3 text-sm text-ink outline-none focus:border-accent"
              />
              <Button type="submit" size="sm" disabled={sendingInput} className="gap-1.5">
                <Send size={13} />
                发送
              </Button>
            </form>
          )}

          <div className="flex justify-end gap-2 pt-1">
            {authSnap?.state === 'failed' && (
              <Button size="sm" onClick={handleStartAuth} className="gap-1.5">
                <KeyRound size={13} />
                重试认证
              </Button>
            )}
            <Button variant="secondary" size="sm" onClick={handleCloseAuth}>
              {authSnap?.state === 'running' ? '取消' : '关闭'}
            </Button>
          </div>
        </div>
      </Modal>
    </>
  );
}
