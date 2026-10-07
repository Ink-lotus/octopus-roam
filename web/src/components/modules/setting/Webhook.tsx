import { useId, useState } from 'react';
import { useTranslations } from 'use-intl';
import { BellRing, Check, FlaskConical, Pencil, Plus, Trash2, X } from 'lucide-react';
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
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';

type EndpointDraft = Omit<WebhookEndpoint, 'id'>;

const defaultBodyTemplate = `{
  "event": {{ .Event | json }},
  "timestamp": {{ .Timestamp | json }},
  "group_id": {{ .GroupID }},
  "group_name": {{ .GroupName | json }},
  "mode": {{ .Mode | json }},
  "current_channel": {{ .CurrentChannel | json }},
  "failure_streak": {{ .FailureStreak }},
  "failed_channels": {{ .FailedChannels }},
  "threshold": {{ .Threshold }},
  "retry_count": {{ .RetryCount }},
  "last_error": {{ .LastError | json }}
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

export function SettingWebhook() {
    const t = useTranslations('setting.webhook');
    const formTitleId = useId();
    const { data: endpoints = [] } = useWebhookEndpointList();
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
    const [formOpen, setFormOpen] = useState(false);
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
        <Popover open={formOpen} onOpenChange={setFormOpen} modal>
        <section className="min-w-0 space-y-5 rounded-3xl border border-border bg-card p-6">
            <h2 className="flex items-center gap-2 text-lg font-bold text-card-foreground">
                <BellRing className="size-5" />
                {t('title')}
            </h2>

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

            <div className="flex items-center justify-between border-t border-border pt-4">
                <h3 className="text-sm font-semibold">{t('endpoints')}</h3>
                <PopoverTrigger asChild>
                    <Button type="button" size="sm" onClick={openCreate}>
                        <Plus className="size-4" />
                        {t('add')}
                    </Button>
                </PopoverTrigger>
            </div>

            <div className="h-36 overflow-y-auto overscroll-contain">
            {endpoints.length === 0 ? (
                <p className="text-sm text-muted-foreground">{t('empty')}</p>
            ) : (
                <ul className="divide-y divide-border">
                    {endpoints.map((endpoint) => (
                        <li key={endpoint.id} className="space-y-2 py-3 first:pt-0 last:pb-0">
                            <div className="flex flex-wrap items-center justify-between gap-3">
                                <div className="flex min-w-0 flex-1 items-center gap-3">
                                    <Switch checked={endpoint.enabled} onCheckedChange={(checked) => void toggleEndpoint(endpoint, checked)} aria-label={t('enabled')} />
                                    <div className="min-w-0">
                                        <div className="truncate text-sm font-medium">{endpoint.name}</div>
                                        <div className="truncate text-xs text-muted-foreground" title={endpoint.url}>{endpoint.method} · {endpoint.url}</div>
                                    </div>
                                </div>
                                <div className="flex shrink-0 items-center gap-1">
                                    <Button type="button" variant="ghost" size="icon" title={t('test')} aria-label={t('test')} disabled={testEndpoint.isPending} onClick={() => void sendTest(endpoint.id)}>
                                        <FlaskConical className="size-4" />
                                    </Button>
                                    <Button type="button" variant="ghost" size="icon" title={t('edit')} aria-label={t('edit')} onClick={() => openEdit(endpoint)}>
                                        <Pencil className="size-4" />
                                    </Button>
                                    <Button type="button" variant="ghost" size="icon" title={t('delete')} aria-label={t('delete')} onClick={() => void removeEndpoint(endpoint.id)}>
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

            <PopoverContent align="end" side="top" sideOffset={12} collisionPadding={16}
                aria-labelledby={formTitleId}
                className="w-[min(36rem,calc(100vw-2rem))] max-h-[min(80vh,var(--radix-popover-content-available-height))] overflow-y-auto overscroll-contain rounded-3xl bg-card p-5 shadow-xl">
                <form className="space-y-4" onSubmit={(event) => { event.preventDefault(); void saveEndpoint(); }}>
                    <div className="flex items-center justify-between gap-3">
                        <h3 id={formTitleId} className="text-sm font-semibold">{editingId === null ? t('createTitle') : t('editTitle')}</h3>
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
            </PopoverContent>
        </section>
        </Popover>
    );
}
