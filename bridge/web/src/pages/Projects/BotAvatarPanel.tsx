import { useEffect, useRef, useState } from 'react';
import { Upload, Check, UserRound, X } from 'lucide-react';
import { Button, Card, Input } from '@/components/ui';
import { getProjectAvatar, updateProject, updateProjectAvatar } from '@/api/projects';

const MAX_BYTES = 8 * 1024 * 1024;
const ALLOWED_TYPES = new Set(['image/png', 'image/jpeg', 'image/gif', 'image/webp']);

interface BotAvatarPanelProps {
  project: string;
  currentDisplayName?: string;
  onNameSynced?: (newName: string) => void;
}

export default function BotAvatarPanel({ project, currentDisplayName = '', onNameSynced }: BotAvatarPanelProps) {
  const inputRef = useRef<HTMLInputElement>(null);
  const [supported, setSupported] = useState(false);
  const [supportsName, setSupportsName] = useState(false);
  const [currentURL, setCurrentURL] = useState('');
  const [previewURL, setPreviewURL] = useState('');
  const [fileName, setFileName] = useState('');
  const [uploading, setUploading] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState('');

  const [discordName, setDiscordName] = useState(currentDisplayName);
  const [syncingName, setSyncingName] = useState(false);
  const [nameSavedMsg, setNameSavedMsg] = useState('');
  const [nameError, setNameError] = useState('');

  useEffect(() => {
    let active = true;
    setSupported(false);
    setSupportsName(false);
    setCurrentURL('');
    setPreviewURL('');
    setFileName('');
    setSaved(false);
    setError('');
    setNameSavedMsg('');
    setNameError('');
    setDiscordName(currentDisplayName);
    getProjectAvatar(project)
      .then((res) => {
        if (!active) return;
        setSupported(Boolean(res.supported));
        setSupportsName(Boolean(res.supports_name));
        setCurrentURL(res.avatar_url || '');
        if (!currentDisplayName && res.bot_username) {
          setDiscordName(res.bot_username);
        }
      })
      .catch(() => {
        if (active) setSupported(false);
      });
    return () => { active = false; };
  }, [project, currentDisplayName]);

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
      const res = await updateProjectAvatar(project, { data_url: previewURL });
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

  const handleSyncName = async () => {
    const trimmed = discordName.trim();
    if (!trimmed) {
      setNameError('名称不能为空');
      return;
    }
    if ([...trimmed].length > 32) {
      setNameError('Discord 名称不能超过 32 个字符');
      return;
    }
    setSyncingName(true);
    setNameSavedMsg('');
    setNameError('');
    try {
      const res = await updateProjectAvatar(project, { name: trimmed });
      await updateProject(project, { display_name: trimmed, name_customized: true });
      onNameSynced?.(trimmed);
      if (res.global_name_updated && (res.updated_guilds ?? 0) > 0) {
        setNameSavedMsg('已同步到 Bothouse、Discord 服务器昵称与全局用户名');
      } else if ((res.updated_guilds ?? 0) > 0) {
        setNameSavedMsg('已同步到 Bothouse 与 Discord 服务器昵称');
      } else {
        setNameSavedMsg('已同步到 Bothouse 与 Discord 用户名');
      }
    } catch (e: unknown) {
      setNameError(e instanceof Error ? e.message : String(e));
    } finally {
      setSyncingName(false);
    }
  };

  const shownAvatar = previewURL || currentURL;

  return (
    <Card>
      <div className="space-y-5">
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
              <h3 className="text-sm font-semibold text-ink">Discord 机器人资料</h3>
              <p className="mt-0.5 text-xs leading-relaxed text-ink-soft">
                选择本地图片后，后台直接使用该角色的 Bot Token 同步更新到 Discord（支持 PNG / JPG / WebP / GIF，最大 8MB）。
              </p>
              {fileName && (
                <p className="mt-1 truncate text-xs text-accent-text">已选文件：{fileName}</p>
              )}
              {saved && (
                <p className="mt-1 inline-flex items-center gap-1 text-xs text-emerald-600 dark:text-emerald-400">
                  <Check size={13} /> 头像已同步到 Discord，客户端几秒内刷新
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

        {supportsName && (
          <div className="border-t border-hairline pt-4">
            <div className="flex flex-col gap-2 sm:flex-row sm:items-end max-w-lg">
              <div className="flex-1">
                <Input
                  label="Discord 显示名称（同步更新服务器昵称与 Bothouse 显示名）"
                  value={discordName}
                  onChange={(e) => { setDiscordName(e.target.value); setNameSavedMsg(''); setNameError(''); }}
                  maxLength={32}
                  placeholder="输入角色显示名称"
                />
              </div>
              <Button
                type="button"
                variant="secondary"
                size="sm"
                loading={syncingName}
                disabled={!discordName.trim()}
                onClick={handleSyncName}
                className="shrink-0 sm:mb-0.5"
              >
                <Check size={14} />
                同步名称到 Discord
              </Button>
            </div>
            {nameSavedMsg && (
              <p className="mt-1.5 inline-flex items-center gap-1 text-xs text-emerald-600 dark:text-emerald-400">
                <Check size={13} /> {nameSavedMsg}
              </p>
            )}
            {nameError && (
              <p className="mt-1.5 text-xs text-red-500">{nameError}</p>
            )}
          </div>
        )}
      </div>
    </Card>
  );
}
