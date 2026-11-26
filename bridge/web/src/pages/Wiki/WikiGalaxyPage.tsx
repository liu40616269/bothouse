import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { Sparkles, RotateCw, ExternalLink, ArrowLeft, Award, Loader2 } from 'lucide-react';
import { Button, PageHeader } from '@/components/ui';
import { api } from '@/api/client';
import { collabPageUrl } from '@/lib/collabPages';
import { goToDsh } from '@/lib/openDsh';
import { DSHNotice } from '@/components/DSHNotice';

/**
 * Wiki 星河 as a bothouse sub-page (#145).
 *
 * The galaxy itself is the collab host's own page at `/collab/wiki-galaxy` (dsh/collab/
 * wiki-galaxy.mjs): one read-only fetch, no state, no write path. Re-implementing the canvas
 * here would fork the graph layout and the wiki scan, so this page frames the existing page
 * instead — same origin, same sign-in, no token reaches the frame.
 */
export default function WikiGalaxyPage() {
  const { t } = useTranslation();
  const src = collabPageUrl('wiki-galaxy', undefined, api.getToken());
  // Reload means "ask the framed page again", not "re-mount the panel": a changing key
  // re-creates the frame, which refetches the graph.
  const [reload, setReload] = useState(0);
  const embedded = window.self !== window.top;
  const [availability, setAvailability] = useState<'checking' | 'running' | 'stopped' | 'error'>('checking');
  const [opening, setOpening] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    // Check the actual collab page, not the management server's DSH process state:
    // an embedded panel can be served by a different, healthy DSH host.
    // GET is intentional: the collab galaxy route does not handle HEAD.
    void fetch(src, { signal: controller.signal, cache: 'no-store' }).then((response) => {
      if (!controller.signal.aborted) {
        // 来源：HTTP 503 is the proxy's explicit DSH-not-running response.
        setAvailability(response.ok ? 'running' : response.status === 503 ? 'stopped' : 'error');
      }
    }).catch(() => {
      if (!controller.signal.aborted) setAvailability('error');
    });
    return () => controller.abort();
  }, [src, reload]);

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4" data-testid="wiki-galaxy-page">
      <Link
        to="/"
        className="inline-flex w-fit items-center gap-1.5 rounded-control px-2.5 py-1.5 text-sm text-ink-soft transition-colors duration-base hover:bg-surface-sunken hover:text-ink focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-text"
      >
        <ArrowLeft size={14} />
        {t('nav.dashboard')}
      </Link>
      <PageHeader
        icon={Sparkles}
        title={t('wikiGalaxy.title', 'Wiki 星河')}
        description={t('wikiGalaxy.subtitle', '把 wiki 画成星系：每页一颗星，点星看内容。只读，不改任何文件。')}
        actions={
          <>
            <Button variant="secondary" size="sm" onClick={() => { setAvailability('checking'); setReload((n) => n + 1); }}>
              <RotateCw size={14} />
              {t('common.refresh', '刷新')}
            </Button>
            {availability === 'running' && (
              <a
                className="inline-flex items-center gap-1.5 rounded-control px-2.5 py-1.5 text-sm text-ink-soft transition-colors duration-base hover:bg-surface-sunken hover:text-ink"
                href={src}
                target="_blank"
                rel="noreferrer"
              >
                <ExternalLink size={14} />
                {t('wikiGalaxy.open', '单独打开')}
              </a>
            )}
          </>
        }
      />
      <div className="flex min-h-[60vh] flex-1 flex-col overflow-hidden rounded-card border border-hairline bg-surface">
        {availability !== 'running' ? (
          <div className="flex flex-1 flex-col items-center justify-center px-6 py-16 text-center">
            {availability === 'checking' ? (
              <div role="status" className="flex flex-col items-center gap-3 text-ink-soft">
                <Loader2 size={24} className="animate-spin text-accent-text" />
                <p className="text-sm">{t('common.loading')}</p>
              </div>
            ) : (
              <div className="flex max-w-md flex-col items-center gap-4">
                <div className="grid h-14 w-14 place-items-center rounded-2xl bg-accent-wash text-accent-text ring-1 ring-accent/20">
                  <Sparkles size={26} strokeWidth={1.7} />
                </div>
                <div className="space-y-1.5">
                  <h2 className="text-base font-semibold text-ink">
                    {availability === 'stopped'
                      ? t('wikiGalaxy.dshStoppedTitle', 'Wiki 星河需要 DSH 服务运行')
                      : t('wikiGalaxy.dshErrorTitle', '暂时无法加载 Wiki 星河')}
                  </h2>
                  <p className="text-sm leading-relaxed text-ink-soft">
                    {availability === 'stopped'
                      ? t('wikiGalaxy.dshStoppedDesc', '当前未检测到运行中的 DSH 服务，无法渲染星系图谱。你可以直接前往「记忆论坛」浏览记忆，或启动 DSH 后刷新此页。')
                      : t('wikiGalaxy.dshErrorDesc', '连接 DSH 星河页面失败，请稍后刷新重试。')}
                  </p>
                </div>
                {embedded && <DSHNotice status={availability} />}
                <div className="mt-1 flex flex-wrap items-center justify-center gap-2.5">
                  {embedded ? (
                    <Button
                      variant="primary"
                      size="sm"
                      disabled={opening}
                      onClick={() => {
                        setOpening(true);
                        void goToDsh(src).finally(() => setOpening(false));
                      }}
                    >
                      {t('dshManage.openDsh')}
                    </Button>
                  ) : (
                    <Link
                      to="/forum"
                      className="inline-flex h-9 items-center gap-1.5 rounded-control bg-accent px-4 text-sm font-medium text-white shadow-ui-gloss-1 transition-colors duration-base hover:bg-accent-dim dark:text-black yellow:text-gray-900"
                    >
                      <Award size={15} />
                      {t('nav.forum')}
                    </Link>
                  )}
                  <Button
                    variant="secondary"
                    size="sm"
                    onClick={() => { setAvailability('checking'); setReload((n) => n + 1); }}
                  >
                    <RotateCw size={14} />
                    {t('common.refresh', '刷新')}
                  </Button>
                </div>
              </div>
            )}
          </div>
        ) : (
          <iframe
            key={reload}
            className="h-full min-h-[70vh] w-full flex-1 border-0"
            src={src}
            title={t('wikiGalaxy.title', 'Wiki 星河')}
            loading="lazy"
            referrerPolicy="no-referrer"
            data-testid="wiki-galaxy-frame"
          />
        )}
      </div>
    </div>
  );
}
