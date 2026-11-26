import { useEffect, useRef, useState } from 'react';
import { Upload, Check, UserRound, X } from 'lucide-react';
import { Button, Card } from '@/components/ui';
import { getProjectAvatar, updateProjectAvatar } from '@/api/projects';

const MAX_BYTES = 8 * 1024 * 1024;
const ALLOWED_TYPES = new Set(['image/png', 'image/jpeg', 'image/gif', 'image/webp']);

export default function BotAvatarPanel({ project }: { project: string }) {
  const inputRef = useRef<HTMLInputElement>(null);
  const [supported, setSupported] = useState(false);
  const [currentURL, setCurrentURL] = useState('');
  const [previewURL, setPreviewURL] = useState('');
  const [fileName, setFileName] = useState('');
  const [uploading, setUploading] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    let active = true;
    setSupported(false);
    setCurrentURL('');
    setPreviewURL('');
    setFileName('');
    setSaved(false);
    setError('');
    getProjectAvatar(project)
      .then((res) => {
        if (!active) return;
        setSupported(Boolean(res.supported));
        setCurrentURL(res.avatar_url || '');
      })
      .catch(() => {
        if (active) setSupported(false);
      });
    return () => { active = false; };
  }, [project]);

  if (!supported) return null;

  const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    e.target.value = '';
    if (!file) return;
    setSaved(false);
    setError('');
    if (!ALLOWED_TYPES.has(file.type)) {
      setError('仅支持 PNG、JPG、WebP 或 GIF 格式图片');
      return;
    }
    if (file.size > MAX_BYTES) {
      setError('图片不能超过 8MB');
      return;
    }
    const reader = new FileReader();
    reader.onload = () => {
      if (typeof reader.result === 'string') {
        setPreviewURL(reader.result);
        setFileName(file.name);
      }
    };
    reader.onerror = () => setError('读取本地图片失败');
    reader.readAsDataURL(file);
  };

  const handleUpload = async () => {
    if (!previewURL) return;
    setUploading(true);
    setSaved(false);
    setError('');
    try {
      const res = await updateProjectAvatar(project, previewURL);
      if (res.avatar_url) {
        setCurrentURL(res.avatar_url);
      }
      setPreviewURL('');
      setFileName('');
      setSaved(true);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setUploading(false);
    }
  };

  const shownAvatar = previewURL || currentURL;

  return (
    <Card>
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex items-center gap-4 min-w-0">
          <div className="relative h-16 w-16 shrink-0 overflow-hidden rounded-full bg-surface-sunken ring-2 ring-hairline">
            {shownAvatar ? (
              <img src={shownAvatar} alt="Bot avatar" className="h-full w-full object-cover" />
            ) : (
              <div className="grid h-full w-full place-items-center text-ink-mute">
                <UserRound size={26} />
              </div>
            )}
          </div>
          <div className="min-w-0">
            <h3 className="text-sm font-semibold text-ink">Discord 机器人头像</h3>
            <p className="mt-0.5 text-xs leading-relaxed text-ink-soft">
              选择本地图片后，后台直接使用该角色的 Bot Token 同步更新到 Discord（支持 PNG / JPG / WebP / GIF，最大 8MB）。
            </p>
            {fileName && (
              <p className="mt-1 truncate text-xs text-accent-text">已选文件：{fileName}</p>
            )}
            {saved && (
              <p className="mt-1 inline-flex items-center gap-1 text-xs text-emerald-600 dark:text-emerald-400">
                <Check size={13} /> 已同步到 Discord，客户端几秒内刷新
              </p>
            )}
            {error && (
              <p className="mt-1 text-xs text-red-500">{error}</p>
            )}
          </div>
        </div>

        <div className="flex shrink-0 flex-wrap items-center gap-2">
          <input
            ref={inputRef}
            type="file"
            accept="image/png,image/jpeg,image/webp,image/gif"
            className="hidden"
            onChange={handleFileChange}
          />
          <Button
            type="button"
            variant="secondary"
            size="sm"
            disabled={uploading}
            onClick={() => inputRef.current?.click()}
          >
            <Upload size={14} />
            {previewURL ? '重新选择' : '更换头像'}
          </Button>
          {previewURL && (
            <>
              <Button
                type="button"
                variant="primary"
                size="sm"
                loading={uploading}
                onClick={handleUpload}
              >
                <Check size={14} />
                同步到 Discord
              </Button>
              <Button
                type="button"
                variant="secondary"
                size="sm"
                disabled={uploading}
                onClick={() => { setPreviewURL(''); setFileName(''); setError(''); }}
                aria-label="取消"
              >
                <X size={14} />
              </Button>
            </>
          )}
        </div>
      </div>
    </Card>
  );
}
