import { useState } from 'react';
import { useTranslations } from 'use-intl';
import { BellRing, Check, FlaskConical, LoaderCircle, Maximize2, Pencil, Plus, Trash2, X } from 'lucide-react';
import { toast } from 'sonner';
import { useSettingList, useSetSetting } from '@/api/setting';
import {
    useCreateWebhookEndpoint,
    useDeleteWebhookEndpoint,
    useTestWebhookEndpoint,
    useUpdateWebhookEndpoint,
    useWebhookEndpointList,
    type WebhookEndpoint,
} from '@/api/webhook';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import { Dialog } from 'radix-ui';
import {
    MorphingDialog,
    MorphingDialogContainer,
    MorphingDialogContent,
    MorphingDialogTrigger,
    useMorphingDialog,
} from '@/components/ui/morphing-dialog';

type EndpointDraft = Omit<WebhookEndpoint, 'id'>;

const defaultBodyTemplate = String.raw`{
  "title": {{ if eq .Event "failure" }}"🚨 Octopus Roam 故障告警"{{ else if eq .Event "recovery" }}"✅ Octopus Roam 恢复通知"{{ else if eq .Event "test" }}"🧪 Octopus Roam 测试通知"{{ else }}"📣 Octopus Roam 通知"{{ end }},
  "content": {{ if eq .Event "test" }}
    {{ printf "🧪 测试预览\n以下分组、渠道、计数和错误为示例数据。\n\n事件：测试通知\n🕒 时间：%s\n\n📦 分组：示例分组（ID：1）\n⚙️ 模式：手动模式\n🔌 当前渠道：示例渠道\n\n📊 故障统计\n连续失败：10 次\n失效成员：不适用\n通知阈值：10 次\n重试次数：9 次\n\n📝 最近错误\n示例错误：上游请求超时" .Timestamp | json }}
  {{ else }}
    {{ $event := .Event }}{{ if eq .Event "failure" }}{{ $event = "故障告警" }}{{ else if eq .Event "recovery" }}{{ $event = "恢复通知" }}{{ end }}
    {{ $mode := printf "%s" .Mode }}{{ $failures := "不适用" }}{{ $failed := "不适用" }}{{ $threshold := printf "%d" .Threshold }}
    {{ if eq .Mode "manual" }}{{ $mode = "手动模式" }}{{ $failures = printf "%d 次" .FailureStreak }}{{ $threshold = printf "%d 次" .Threshold }}
    {{ else if eq .Mode "failover" }}{{ $mode = "故障转移" }}{{ $failed = printf "%d 个" .FailedChannels }}{{ $threshold = printf "%d 个" .Threshold }}{{ end }}
    {{ printf "事件：%s\n🕒 时间：%s\n\n📦 分组：%s（ID：%d）\n⚙️ 模式：%s\n🔌 当前渠道：%s\n\n📊 故障统计\n连续失败：%s\n失效成员：%s\n通知阈值：%s\n重试次数：%d 次\n\n📝 最近错误\n%s" $event .Timestamp (or .GroupName "未提供") .GroupID (or $mode "未提供") (or .CurrentChannel "未提供") $failures $failed $threshold .RetryCount (or .LastError "无错误详情") | json }}
  {{ end }},
  "from": "octopus-roam"
}`;

const emptyDraft: EndpointDraft = {
    name: '',
    enabled: true,
    url: '',
    method: 'POST',
    content_type: 'application/json',
    headers: {},
    body_template: defaultBodyTemplate,
    timeout_seconds: 10,
};

const thresholdKeys = {
    manual: 'webhook_manual_failure_threshold',
    failover: 'webhook_failover_channel_threshold',
} as const;

function WebhookPanel({ expanded = false, onClose, titleId, onFormOpenChange }: {
    expanded?: boolean;
    onClose?: () => void;
    titleId?: string;
    onFormOpenChange?: (open: boolean) => void;
}) {
    const t = useTranslations('setting.webhook');
    const { data: endpoints = [], isPending: endpointsLoading, isError: endpointsFailed } = useWebhookEndpointList();
    const { data: settings } = useSettingList();
    const setSetting = useSetSetting();
    const createEndpoint = useCreateWebhookEndpoint();
    const updateEndpoint = useUpdateWebhookEndpoint();
    const deleteEndpoint = useDeleteWebhookEndpoint();
    const testEndpoint = useTestWebhookEndpoint();
    const manualSetting = settings?.find((setting) => setting.key === thresholdKeys.manual)?.value ?? '10';
    const failoverSetting = settings?.find((setting) => setting.key === thresholdKeys.failover)?.value ?? '3';
    const [manualDraft, setManualDraft] = useState<string | null>(null);
    const [failoverDraft, setFailoverDraft] = useState<string | null>(null);
    const manualThreshold = manualDraft ?? manualSetting;
    const failoverThreshold = failoverDraft ?? failoverSetting;
    const [formOpen, setFormOpenState] = useState(false);
    const setFormOpen = (open: boolean) => {
        setFormOpenState(open);
        onFormOpenChange?.(open);
    };
    const [editingId, setEditingId] = useState<number | null>(null);
    const [draft, setDraft] = useState<EndpointDraft>(emptyDraft);
    const [headersText, setHeadersText] = useState('{}');
    const [testResults, setTestResults] = useState<Record<number, string>>({});

    const openCreate = () => {
        setEditingId(null);
        setDraft(emptyDraft);
        setHeadersText('{}');
        setFormOpen(true);
    };

    const openEdit = (endpoint: WebhookEndpoint) => {
        const { id, ...values } = endpoint;
        setEditingId(id);
        setDraft(values);
        setHeadersText(JSON.stringify(values.headers ?? {}, null, 2));
        setFormOpen(true);
    };

    const saveEndpoint = async () => {
        let headers: Record<string, string>;
        try {
            const parsed: unknown = JSON.parse(headersText);
            if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object' || Object.values(parsed).some((value) => typeof value !== 'string')) {
                throw new Error(t('invalidHeaders'));
            }
            headers = parsed as Record<string, string>;
        } catch (error) {
            toast.error(error instanceof Error ? error.message : t('invalidHeaders'));
            return;
        }
        const endpoint = { ...draft, headers };
        try {
            if (editingId === null) {
                await createEndpoint.mutateAsync({ endpoint, body: endpoint });
                toast.success(t('created'));
            } else {
                await updateEndpoint.mutateAsync({ endpoint: { ...endpoint, id: editingId }, body: endpoint });
                toast.success(t('saved'));
            }
            setFormOpen(false);
        } catch (error) {
            toast.error(error instanceof Error ? error.message : t('saveFailed'));
        }
    };

    const saveThreshold = async (key: string, value: string, initial: string, clearDraft: () => void) => {
        const normalized = value.trim();
        if (!normalized || Number(normalized) < 1 || !Number.isInteger(Number(normalized))) {
            toast.error(t('invalidThreshold'));
            return;
        }
        if (normalized === initial) return;
        try {
            await setSetting.mutateAsync({ key, value: normalized });
            clearDraft();
            toast.success(t('saved'));
        } catch (error) {
            toast.error(error instanceof Error ? error.message : t('saveFailed'));
        }
    };

    const toggleEndpoint = async (endpoint: WebhookEndpoint, enabled: boolean) => {
        try {
            await updateEndpoint.mutateAsync({ endpoint: { ...endpoint, enabled }, body: { ...endpoint, enabled } });
        } catch (error) {
            toast.error(error instanceof Error ? error.message : t('saveFailed'));
        }
    };

    const removeEndpoint = async (id: number) => {
        if (!window.confirm(t('deleteConfirm'))) return;
        try {
            await deleteEndpoint.mutateAsync(id);
            toast.success(t('deleted'));
        } catch (error) {
            toast.error(error instanceof Error ? error.message : t('deleteFailed'));
        }
    };

    const sendTest = async (id: number) => {
        try {
            const result = await testEndpoint.mutateAsync(id);
            const message = result.success ? t('testSuccess', { status: result.status_code ?? 0 }) : result.error ?? t('testFailed');
            setTestResults((previous) => ({ ...previous, [id]: message }));
            if (result.success) toast.success(message);
            else toast.error(message);
        } catch (error) {
            const message = error instanceof Error ? error.message : t('testFailed');
            setTestResults((previous) => ({ ...previous, [id]: message }));
            toast.error(message);
        }
    };

    return (
        <Dialog.Root open={formOpen} onOpenChange={setFormOpen}>
            <section className={`relative flex min-w-0 flex-col gap-5 rounded-3xl border border-border bg-card p-6 ${formOpen ? 'z-50' : ''} ${expanded ? 'h-[calc(100dvh-2rem)] max-h-[56rem] w-[calc(100vw-2rem)] max-w-2xl' : ''}`}>
                <div className="flex shrink-0 items-center justify-between gap-3">
                    <h2 id={titleId} className="flex min-w-0 items-center gap-2 text-lg font-bold text-card-foreground">
                        <BellRing className="size-5 shrink-0" />
                        {expanded ? t('endpoints') : t('title')}
                    </h2>
                    <div className="flex shrink-0 items-center gap-2">
                        <Dialog.Trigger asChild>
                            <button type="button" title={t('add')} aria-label={t('add')} onClick={openCreate}
                                className="flex size-9 items-center justify-center rounded-lg bg-muted/60 text-muted-foreground transition-colors hover:bg-muted disabled:opacity-50"
                                disabled={formOpen || createEndpoint.isPending || updateEndpoint.isPending}>
                                <Plus className="size-4" />
                            </button>
                        </Dialog.Trigger>
                        {expanded ? (
                            <button type="button" title={t('close')} aria-label={t('close')} onClick={onClose}
                                className="flex size-9 items-center justify-center rounded-lg bg-muted/60 text-muted-foreground transition-colors hover:bg-muted">
                                <X className="size-4" />
                            </button>
                        ) : (
                            <MorphingDialog>
                                {formOpen ? (
                                    <button type="button" disabled aria-label={t('viewAll')}
                                        className="flex size-9 items-center justify-center rounded-lg bg-muted/60 text-muted-foreground opacity-50">
                                        <Maximize2 className="size-4" />
                                    </button>
                                ) : (
                                    <MorphingDialogTrigger aria-label={t('viewAll')} className="flex size-9 items-center justify-center rounded-lg bg-muted/60 text-muted-foreground transition-colors hover:bg-muted">
                                        <Maximize2 className="size-4" />
                                        <span className="sr-only">{t('viewAll')}</span>
                                    </MorphingDialogTrigger>
                                )}
                                <MorphingDialogContainer>
                                    <WebhookDialogPanel />
                                </MorphingDialogContainer>
                            </MorphingDialog>
                        )}
                    </div>
                </div>

                {!expanded && (
                    <div className="grid gap-3 sm:grid-cols-2">
                        <label className="space-y-1.5 text-sm">
                            <span>{t('manualThreshold')}</span>
                            <Input
                                type="number"
                                min={1}
                                step={1}
                                value={manualThreshold}
                                onChange={(event) => setManualDraft(event.target.value)}
                                onBlur={() => void saveThreshold(thresholdKeys.manual, manualThreshold, manualSetting, () => setManualDraft(null))}
                            />
                        </label>
                        <label className="space-y-1.5 text-sm">
                            <span>{t('failoverThreshold')}</span>
                            <Input
                                type="number"
                                min={1}
                                step={1}
                                value={failoverThreshold}
                                onChange={(event) => setFailoverDraft(event.target.value)}
                                onBlur={() => void saveThreshold(thresholdKeys.failover, failoverThreshold, failoverSetting, () => setFailoverDraft(null))}
                            />
                        </label>
                    </div>
                )}

                {!expanded && (
                    <div className="border-t border-border pt-4">
                        <h3 className="text-sm font-semibold">{t('endpoints')}</h3>
                    </div>
                )}

                <div className={`${expanded ? 'min-h-0 flex-1' : 'h-36'} overflow-y-auto overscroll-contain`}>
                    {endpointsLoading ? (
                        <p role="status" className="flex items-center gap-2 text-sm text-muted-foreground"><LoaderCircle className="size-4 animate-spin" />{t('loading')}</p>
                    ) : endpointsFailed ? (
                        <p role="alert" className="text-sm text-destructive">{t('loadFailed')}</p>
                    ) : endpoints.length === 0 ? (
                        <p className="text-sm text-muted-foreground">{t('empty')}</p>
                    ) : (
                        <ul className="space-y-2">
                            {endpoints.map((endpoint) => (
                                <li key={endpoint.id} className="space-y-2 rounded-xl bg-muted/50 p-3">
                                    <div className="flex flex-wrap items-center justify-between gap-3">
                                        <div className="flex min-w-0 flex-1 items-center gap-3">
                                            <Switch checked={endpoint.enabled} onCheckedChange={(checked) => void toggleEndpoint(endpoint, checked)} aria-label={t('enabled')} />
                                            <div className="min-w-0">
                                                <div className="truncate text-sm font-medium">{endpoint.name}</div>
                                                <div className="truncate text-xs text-muted-foreground" title={endpoint.url}>{endpoint.method} · {endpoint.url}</div>
                                            </div>
                                        </div>
                                        <div className="flex shrink-0 items-center gap-1">
                                            <Button type="button" variant="ghost" size="icon" title={t('test')} aria-label={t('test')} disabled={testEndpoint.isPending} onClick={() => void sendTest(endpoint.id)}
                                                className="size-8 rounded-lg bg-muted/60 text-muted-foreground hover:bg-muted hover:text-foreground dark:hover:bg-muted">
                                                <FlaskConical className="size-4" />
                                            </Button>
                                            <Button type="button" variant="ghost" size="icon" title={t('edit')} aria-label={t('edit')} onClick={() => openEdit(endpoint)}
                                                className="size-8 rounded-lg bg-muted/60 text-muted-foreground hover:bg-muted hover:text-foreground dark:hover:bg-muted">
                                                <Pencil className="size-4" />
                                            </Button>
                                            <Button type="button" variant="ghost" size="icon" title={t('delete')} aria-label={t('delete')} onClick={() => void removeEndpoint(endpoint.id)}
                                                className="size-8 rounded-lg bg-destructive/10 text-destructive hover:bg-destructive hover:text-destructive-foreground dark:hover:bg-destructive">
                                                <Trash2 className="size-4" />
                                            </Button>
                                        </div>
                                    </div>
                                    {testResults[endpoint.id] && <p className="text-xs text-muted-foreground">{testResults[endpoint.id]}</p>}
                                </li>
                            ))}
                        </ul>
                    )}
                </div>

                {/* 表单以面板为定位和宽度约束容器。 */}
                <Dialog.Content
                    aria-describedby={undefined}
                    onEscapeKeyDown={(event) => event.stopPropagation()}
                    className="absolute left-1/2 top-1/2 z-50 m-0 w-[min(36rem,calc(100%-2rem))] max-h-[min(80dvh,calc(100%-2rem))] -translate-x-1/2 -translate-y-1/2 overflow-y-auto overscroll-contain rounded-3xl border border-border bg-card p-5 shadow-xl outline-none">
                    <form className="space-y-4" onSubmit={(event) => { event.preventDefault(); void saveEndpoint(); }}>
                        <div className="flex items-center justify-between gap-3">
                            <Dialog.Title asChild>
                                <h3 className="text-sm font-semibold">{editingId === null ? t('createTitle') : t('editTitle')}</h3>
                            </Dialog.Title>
                            <Button type="button" variant="ghost" size="icon" title={t('cancel')} aria-label={t('cancel')} onClick={() => setFormOpen(false)}>
                                <X className="size-4" />
                            </Button>
                        </div>
                        <div className="grid gap-3 sm:grid-cols-2">
                            <label className="space-y-1.5 text-sm">
                                <span>{t('name')}</span>
                                <Input value={draft.name} onChange={(event) => setDraft({ ...draft, name: event.target.value })} maxLength={120} required />
                            </label>
                            <label className="space-y-1.5 text-sm">
                                <span>{t('url')}</span>
                                <Input type="url" value={draft.url} onChange={(event) => setDraft({ ...draft, url: event.target.value })} autoComplete="off" required />
                            </label>
                            <label className="space-y-1.5 text-sm">
                                <span>{t('method')}</span>
                                <select className="h-9 w-full rounded-md border border-input bg-transparent px-3 text-sm" value={draft.method} onChange={(event) => setDraft({ ...draft, method: event.target.value })}>
                                    {['POST', 'GET', 'PUT', 'PATCH', 'DELETE'].map((method) => <option key={method} value={method}>{method}</option>)}
                                </select>
                            </label>
                            <label className="space-y-1.5 text-sm">
                                <span>{t('contentType')}</span>
                                <Input value={draft.content_type} onChange={(event) => setDraft({ ...draft, content_type: event.target.value })} />
                            </label>
                            <label className="space-y-1.5 text-sm">
                                <span>{t('timeout')}</span>
                                <Input type="number" min={1} max={120} step={1} required value={draft.timeout_seconds} onChange={(event) => setDraft({ ...draft, timeout_seconds: Number(event.target.value) })} />
                            </label>
                            <label className="flex items-center gap-3 pt-6 text-sm">
                                <Switch checked={draft.enabled} onCheckedChange={(enabled) => setDraft({ ...draft, enabled })} />
                                {t('enabled')}
                            </label>
                        </div>
                        <label className="block space-y-1.5 text-sm">
                            <span>{t('headers')}</span>
                            <textarea className="min-h-24 w-full rounded-md border border-input bg-transparent px-3 py-2 font-mono text-xs" value={headersText} onChange={(event) => setHeadersText(event.target.value)} spellCheck={false} />
                        </label>
                        <label className="block space-y-1.5 text-sm">
                            <span>{t('bodyTemplate')}</span>
                            <textarea className="min-h-40 w-full rounded-md border border-input bg-transparent px-3 py-2 font-mono text-xs" value={draft.body_template} onChange={(event) => setDraft({ ...draft, body_template: event.target.value })} spellCheck={false} />
                        </label>
                        <div className="flex justify-end gap-2">
                            <Button type="button" variant="outline" onClick={() => setFormOpen(false)}><X className="size-4" />{t('cancel')}</Button>
                            <Button type="submit" disabled={createEndpoint.isPending || updateEndpoint.isPending}>
                                <Check className="size-4" />
                                {createEndpoint.isPending || updateEndpoint.isPending ? t('saving') : t('save')}
                            </Button>
                        </div>
                    </form>
                </Dialog.Content>
            </section>
        </Dialog.Root>
    );
}

function WebhookDialogPanel() {
    const { setIsOpen, uniqueId } = useMorphingDialog();
    const t = useTranslations('setting.webhook');
    const [formOpen, setFormOpen] = useState(false);
    return (
        <MorphingDialogContent className="relative" dismissOnClickOutside={!formOpen}>
            <p id={`motion-ui-morphing-dialog-description-${uniqueId}`} className="sr-only">{t('viewAll')}</p>
            <WebhookPanel expanded titleId={`motion-ui-morphing-dialog-title-${uniqueId}`} onClose={() => setIsOpen(false)} onFormOpenChange={setFormOpen} />
        </MorphingDialogContent>
    );
}

export function SettingWebhook() {
    return <WebhookPanel />;
}
