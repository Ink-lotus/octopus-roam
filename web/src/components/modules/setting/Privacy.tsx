import { useEffect, useId, useRef, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useTranslations } from 'use-intl';
import { Dialog } from 'radix-ui';
import { Info, LoaderCircle, Maximize2, Pencil, Play, Plus, Shield, Trash2, X } from 'lucide-react';
import { toast } from 'sonner';
import { ApiError } from '@/api/client';
import { SettingKey, useSetSetting } from '@/api/setting';
import {
    previewPrivacy, privacyRulesKey, privacyValidation, usePrivacyBusy, usePrivacyMutation, usePrivacyRules,
    type PreviewInputType, type PrivacyBuiltin, type PrivacyPreviewResult, type PrivacyRule,
    type PrivacyRuleForm, type PrivacyRules, type RuleKind,
} from '@/api/privacy';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import { cn } from '@/lib/utils';
import {
    MorphingDialog, MorphingDialogContainer, MorphingDialogContent, MorphingDialogTrigger, useMorphingDialog,
} from '@/components/ui/morphing-dialog';

type RuleItem = (PrivacyBuiltin & { source: 'builtin' }) | (PrivacyRule & { source: 'custom' });
type Editor = { rule?: RuleItem; revision: string };
const emptyDraft: PrivacyRuleForm = { name: '', kind: 'field_name', pattern: '', enabled: true };
const iconClass = 'flex size-9 shrink-0 items-center justify-center rounded-lg bg-muted/60 text-muted-foreground transition-colors hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50';
const textareaClass = 'w-full min-w-0 rounded-xl border border-input bg-transparent px-3 py-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50';
const selectClass = 'h-9 w-full min-w-0 rounded-xl border border-input bg-card px-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring';

function useRuleName() {
    const t = useTranslations('setting.privacyProtection');
    return (rule: RuleItem) => rule.source === 'builtin' ? t(`builtins.${rule.key}.name`) : rule.name;
}

function usePrivacyError() {
    const t = useTranslations('setting.privacyProtection');
    return (error: unknown, fallback: string) => {
        const validation = privacyValidation(error);
        if (validation && ['regex', 'empty_match', 'field_name', 'json', 'headers', 'request_limit'].includes(validation.code)) {
            return t(`validation.${validation.code}`);
        }
        return error instanceof Error ? error.message : fallback;
    };
}

function rulesOf(data: PrivacyRules): RuleItem[] {
    return [
        ...data.builtins.map((rule) => ({ ...rule, source: 'builtin' as const })),
        ...data.custom_rules.map((rule) => ({ ...rule, source: 'custom' as const })),
    ];
}

function RuleRow({ rule, busy, onToggle, onOpen, onDelete }: {
    rule: RuleItem;
    busy: boolean;
    onToggle: (rule: RuleItem, enabled: boolean) => void;
    onOpen: (rule: RuleItem, trigger: HTMLButtonElement) => void;
    onDelete: (rule: RuleItem) => void;
}) {
    const t = useTranslations('setting.privacyProtection');
    const name = useRuleName()(rule);
    const summary = rule.source === 'builtin' ? t(`builtins.${rule.key}.summary`) : `${t(`kinds.${rule.kind}`)} · ${rule.pattern}`;
    return (
        <li className="flex min-w-0 items-center gap-3 border-b border-border/60 py-3 last:border-0">
            <Switch checked={rule.enabled} disabled={busy} onCheckedChange={(enabled) => onToggle(rule, enabled)} aria-label={t('toggleRule', { name })} className="shrink-0" />
            <div className="min-w-0 flex-1">
                <div className="flex min-w-0 items-center gap-2">
                    <span className="truncate text-sm font-medium" title={name}>{name}</span>
                    <span className="shrink-0 rounded-full bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground">{t(rule.source)}</span>
                </div>
                <p className="truncate text-xs leading-5 text-muted-foreground" title={summary}>{summary}</p>
            </div>
            <div className="flex shrink-0 items-center gap-1">
                <button type="button" className={cn(iconClass, 'size-8')} title={t(rule.source === 'builtin' ? 'details' : 'edit')}
                    aria-label={t(rule.source === 'builtin' ? 'detailsRule' : 'editRule', { name })} disabled={busy}
                    onClick={(event) => onOpen(rule, event.currentTarget)}>
                    {rule.source === 'builtin' ? <Info className="size-4" /> : <Pencil className="size-4" />}
                </button>
                {rule.source === 'custom' && (
                    <button type="button" className={cn(iconClass, 'size-8 bg-destructive/10 text-destructive hover:bg-destructive hover:text-destructive-foreground')}
                        title={t('delete')} aria-label={t('deleteRule', { name })} disabled={busy} onClick={() => onDelete(rule)}>
                        <Trash2 className="size-4" />
                    </button>
                )}
            </div>
        </li>
    );
}

function PrivacyPanel({ expanded = false, titleId, onClose, onEditorChange }: {
    expanded?: boolean;
    titleId?: string;
    onClose?: () => void;
    onEditorChange?: (open: boolean) => void;
}) {
    const t = useTranslations('setting.privacyProtection');
    const ruleName = useRuleName();
    const query = usePrivacyRules();
    const mutation = usePrivacyMutation();
    const setting = useSetSetting();
    const busy = usePrivacyBusy();
    const [editor, setEditor] = useState<Editor | null>(null);
    const restoreFocus = useRef<HTMLButtonElement | null>(null);
    const masterId = useId();
    const data = query.data;
    const rules = data ? rulesOf(data) : [];
    const enabledCount = rules.filter((rule) => rule.enabled).length;
    const unavailable = !data || query.isError || busy;

    const closeEditor = () => { setEditor(null); onEditorChange?.(false); };
    const openEditor = (rule: RuleItem | undefined, trigger: HTMLButtonElement) => {
        if (!data) return;
        restoreFocus.current = trigger;
        setEditor({ rule, revision: data.revision });
        onEditorChange?.(true);
    };
    const showError = (error: unknown) => {
        toast.error(error instanceof ApiError && error.status === 409 ? t('conflict') : t('saveFailed'), {
            description: error instanceof Error ? error.message : undefined,
        });
    };
    const toggle = async (rule: RuleItem, enabled: boolean) => {
        if (!data || busy) return;
        try {
            await mutation.mutateAsync({ revision: data.revision, action: 'toggle', id: rule.id, enabled });
        } catch (error) { showError(error); }
    };
    const remove = async (rule: RuleItem) => {
        if (!data || busy || rule.source !== 'custom' || !window.confirm(t('deleteConfirm', { name: rule.name }))) return;
        try {
            await mutation.mutateAsync({ revision: data.revision, action: 'delete', id: rule.id });
            toast.success(t('deleted'));
        } catch (error) { showError(error); }
    };
    const toggleMaster = async (enabled: boolean) => {
        if (unavailable) return;
        try { await setting.mutateAsync({ key: SettingKey.PrivacyProtectionEnabled, value: String(enabled) }); }
        catch (error) { showError(error); }
    };

    return (
        <Dialog.Root open={editor !== null} onOpenChange={(open) => { if (!open && !busy) closeEditor(); }}>
            <section className={`flex min-w-0 flex-col gap-4 rounded-3xl border border-border bg-card p-6 ${expanded ? 'h-[calc(100dvh-2rem)] max-h-[52rem] w-[calc(100vw-2rem)] max-w-2xl' : ''}`}>
                <div className="flex shrink-0 flex-wrap items-start justify-between gap-3">
                    <h2 id={titleId} className="flex min-w-0 items-center gap-2 text-lg font-bold text-card-foreground">
                        <Shield className="size-5 shrink-0" />{t('label')}
                    </h2>
                    <div className="flex shrink-0 items-center gap-2">
                        <button type="button" className={iconClass} title={t('add')} aria-label={t('add')}
                            disabled={unavailable || editor !== null || (data?.custom_rules.length ?? 0) >= (data?.limits.custom_rules ?? 0)}
                            onClick={(event) => openEditor(undefined, event.currentTarget)}><Plus className="size-4" /></button>
                        {expanded ? (
                            <button type="button" className={iconClass} title={t('close')} aria-label={t('close')} onClick={onClose}><X className="size-4" /></button>
                        ) : (
                            <MorphingDialog>
                                <MorphingDialogTrigger aria-label={t('viewAll')} className={iconClass}>
                                    <Maximize2 className="size-4" /><span className="sr-only">{t('viewAll')}</span>
                                </MorphingDialogTrigger>
                                <MorphingDialogContainer><PrivacyDialogPanel /></MorphingDialogContainer>
                            </MorphingDialog>
                        )}
                    </div>
                </div>
                <div className="shrink-0 space-y-1">
                    <p className="text-xs font-medium text-muted-foreground">{t('experimental')}</p>
                    <p className="text-xs leading-relaxed text-muted-foreground">{t('hint')}</p>
                </div>
                <div className="shrink-0 space-y-2">
                    <div className="flex items-center justify-between gap-4">
                        <label htmlFor={masterId} className="text-sm font-medium">{t('enabled')}</label>
                        <Switch id={masterId} checked={data?.enabled ?? false} disabled={unavailable} onCheckedChange={(enabled) => void toggleMaster(enabled)} aria-describedby={`${masterId}-hint`} />
                    </div>
                    <p id={`${masterId}-hint`} className="text-xs leading-relaxed text-muted-foreground">
                        {data?.enabled ? t('appliesNext') : t('masterOff')}
                    </p>
                </div>
                <div className="flex shrink-0 flex-wrap items-center justify-between gap-2 border-t border-border pt-3 text-xs">
                    <h3 className="font-semibold">{t('rules')}</h3>
                    {data && <span className="text-muted-foreground">{t('enabledCount', { enabled: enabledCount, total: rules.length })}</span>}
                </div>
                <div className={`${expanded ? 'min-h-0 flex-1' : 'h-36'} overflow-y-auto overscroll-contain`} aria-busy={query.isPending || busy}>
                    {query.isPending && <p role="status" className="flex items-center gap-2 text-sm text-muted-foreground"><LoaderCircle className="size-4 animate-spin" />{t('loading')}</p>}
                    {query.isError && <div role="alert" className="space-y-2 text-sm text-destructive"><p>{t('loadFailed')}</p><Button variant="outline" onClick={() => void query.refetch()}>{t('retry')}</Button></div>}
                    {data && <ul>{rules.map((rule) => <RuleRow key={rule.id} rule={rule} busy={unavailable} onToggle={(rule, enabled) => void toggle(rule, enabled)} onOpen={openEditor} onDelete={(rule) => void remove(rule)} />)}</ul>}
                </div>
                {data && data.custom_rules.length >= data.limits.custom_rules && <p role="status" className="shrink-0 text-xs text-muted-foreground">{t('ruleLimit', { count: data.limits.custom_rules })}</p>}
            </section>
            <Dialog.Portal>
                <Dialog.Overlay className="fixed inset-0 z-[60] bg-black/30 backdrop-blur-xs" />
                <Dialog.Content className="fixed left-1/2 top-1/2 z-[60] max-h-[calc(100dvh-2rem)] w-[calc(100vw-2rem)] max-w-xl -translate-x-1/2 -translate-y-1/2 overflow-y-auto overscroll-contain rounded-3xl border border-border bg-card p-5 shadow-xl outline-none"
                    onEscapeKeyDown={(event) => { event.stopPropagation(); if (busy) event.preventDefault(); }}
                    onInteractOutside={(event) => { if (busy) event.preventDefault(); }}
                    onCloseAutoFocus={(event) => { event.preventDefault(); restoreFocus.current?.focus(); }}>
                    {editor && data && <RuleEditor key={`${editor.rule?.id ?? 'new'}:${editor.revision}`} editor={editor} data={data} busy={busy} onClose={closeEditor} onToggle={(rule, enabled) => void toggle(rule, enabled)} ruleName={ruleName} />}
                </Dialog.Content>
            </Dialog.Portal>
        </Dialog.Root>
    );
}

function RuleEditor({ editor, data, busy, onClose, onToggle, ruleName }: {
    editor: Editor; data: PrivacyRules; busy: boolean; onClose: () => void;
    onToggle: (rule: RuleItem, enabled: boolean) => void;
    ruleName: (rule: RuleItem) => string;
}) {
    const t = useTranslations('setting.privacyProtection');
    const errorMessage = usePrivacyError();
    const client = useQueryClient();
    const mutation = usePrivacyMutation();
    const initial = editor.rule?.source === 'custom' ? editor.rule : emptyDraft;
    const [draft, setDraft] = useState<PrivacyRuleForm>({ name: initial.name, kind: initial.kind, pattern: initial.pattern, enabled: initial.enabled });
    const [revision, setRevision] = useState(editor.revision);
    const [errors, setErrors] = useState<Record<string, string>>({});
    const [conflict, setConflict] = useState(false);
    const saving = useRef(false);
    const formId = useId();
    const builtin = editor.rule?.source === 'builtin' ? data.builtins.find((rule) => rule.id === editor.rule?.id) : undefined;
    const change = <K extends keyof PrivacyRuleForm>(key: K, value: PrivacyRuleForm[K]) => {
        setDraft((current) => ({ ...current, [key]: value }));
        setErrors((current) => ({ ...current, [key]: '' }));
    };
    const save = async () => {
        if (busy || saving.current || builtin) return;
        const validation: Record<string, string> = {};
        if (!draft.name.trim() || Array.from(draft.name).length > data.limits.name_runes) validation.name = t('nameLimit', { count: data.limits.name_runes });
        if (!draft.pattern || new TextEncoder().encode(draft.pattern).length > data.limits.pattern_bytes) validation.pattern = t('patternLimit', { count: data.limits.pattern_bytes });
        setErrors(validation);
        if (Object.keys(validation).length) return;
        saving.current = true;
        try {
            await mutation.mutateAsync(editor.rule
                ? { action: 'update', revision, id: editor.rule.id, rule: draft }
                : { action: 'create', revision, rule: draft });
            toast.success(t('saved'));
            onClose();
        } catch (error) {
            if (error instanceof ApiError && error.status === 409) {
                const latest = client.getQueryData<PrivacyRules>(privacyRulesKey);
                if (latest && latest.revision !== revision) {
                    setRevision(latest.revision);
                    setConflict(true);
                } else {
                    setErrors({ form: t('conflictReloadFailed') });
                }
            } else {
                const validation = privacyValidation(error);
                const field = validation && ['name', 'pattern'].includes(validation.field) ? validation.field : 'form';
                setErrors({ [field]: errorMessage(error, t('saveFailed')) });
            }
        } finally { saving.current = false; }
    };
    const title = builtin ? t('details') : t(editor.rule ? 'edit' : 'add');
    return (
        <div className="space-y-4">
            <div className="flex items-center justify-between gap-3">
                <Dialog.Title className="text-base font-semibold">{title}</Dialog.Title>
                <button type="button" className={iconClass} aria-label={t('cancel')} title={t('cancel')} disabled={busy} onClick={onClose}><X className="size-4" /></button>
            </div>
            <Dialog.Description className="text-xs leading-relaxed text-muted-foreground">{builtin ? t('builtinDescription') : t('customDescription')}</Dialog.Description>
            {builtin ? (
                <>
                    <div className="flex min-w-0 items-center justify-between gap-3">
                        <h3 className="break-words font-medium">{t(`builtins.${builtin.key}.name`)}</h3>
                        <Switch checked={builtin.enabled} disabled={busy} onCheckedChange={(enabled) => onToggle({ ...builtin, source: 'builtin' }, enabled)} aria-label={t('toggleRule', { name: t(`builtins.${builtin.key}.name`) })} />
                    </div>
                    <dl className="space-y-3 text-sm">
                        <div><dt className="text-xs text-muted-foreground">{t('ruleId')}</dt><dd className="break-all font-mono text-xs">{builtin.id}</dd></div>
                        <div><dt className="text-xs text-muted-foreground">{t('target')}</dt><dd className="mt-1 leading-relaxed">{t(`builtins.${builtin.key}.target`)}</dd></div>
                        <div><dt className="text-xs text-muted-foreground">{t('method')}</dt><dd className="mt-1 leading-relaxed">{t(`builtins.${builtin.key}.method`)}</dd></div>
                        <div><dt className="text-xs text-muted-foreground">{t('scopeLabel')}</dt><dd className="mt-1 leading-relaxed">{t('scope')}</dd></div>
                    </dl>
                    <RulePreview data={data} rule={{ ...builtin, source: 'builtin' }} ruleName={ruleName} />
                </>
            ) : (
                <form className="space-y-4" onSubmit={(event) => { event.preventDefault(); void save(); }} noValidate>
                    {conflict && <p role="alert" className="rounded-xl bg-muted p-3 text-sm">{t('conflictDraft')}</p>}
                    {errors.form && <p role="alert" className="text-sm text-destructive">{errors.form}</p>}
                    <div className="space-y-1.5">
                        <label htmlFor={`${formId}-name`} className="text-sm font-medium">{t('name')}</label>
                        <Input id={`${formId}-name`} value={draft.name} onChange={(event) => change('name', event.target.value)} maxLength={data.limits.name_runes * 2} autoComplete="off" required disabled={busy} aria-invalid={!!errors.name} aria-describedby={errors.name ? `${formId}-name-error` : undefined} />
                        {errors.name && <p id={`${formId}-name-error`} role="alert" className="text-xs text-destructive">{errors.name}</p>}
                    </div>
                    <label className="block space-y-1.5 text-sm font-medium"><span>{t('kind')}</span>
                        <select className={selectClass} value={draft.kind} onChange={(event) => change('kind', event.target.value as RuleKind)} disabled={busy}>
                            {(['field_name', 'literal', 'regex'] as const).map((kind) => <option key={kind} value={kind}>{t(`kinds.${kind}`)}</option>)}
                        </select>
                    </label>
                    <div className="space-y-1.5">
                        <label htmlFor={`${formId}-pattern`} className="text-sm font-medium">{t('pattern')}</label>
                        <textarea id={`${formId}-pattern`} className={`${textareaClass} font-mono`} rows={3} value={draft.pattern} onChange={(event) => change('pattern', event.target.value)} autoComplete="off" spellCheck={false} required disabled={busy} aria-invalid={!!errors.pattern} aria-describedby={`${formId}-pattern-hint ${formId}-pattern-error`} />
                        <p id={`${formId}-pattern-hint`} className="text-xs leading-relaxed text-muted-foreground">{t(`hints.${draft.kind}`)}</p>
                        <p id={`${formId}-pattern-error`} role={errors.pattern ? 'alert' : undefined} className="text-xs text-destructive">{errors.pattern}</p>
                    </div>
                    <label className="flex items-center justify-between gap-4 text-sm"><span>{t('ruleEnabled')}</span><Switch checked={draft.enabled} disabled={busy} onCheckedChange={(enabled) => change('enabled', enabled)} aria-label={t('ruleEnabled')} /></label>
                    <RulePreview data={data} rule={editor.rule} draft={draft} ruleName={ruleName} onValidation={(field, message) => setErrors((current) => ({ ...current, [field === 'kind' ? 'form' : field]: message }))} />
                    <div className="flex justify-end gap-2 border-t border-border pt-4">
                        <Button type="button" variant="outline" disabled={busy} onClick={onClose}>{t('cancel')}</Button>
                        <Button type="submit" disabled={busy}>{mutation.isPending && <LoaderCircle className="mr-2 size-4 animate-spin" />}{t('save')}</Button>
                    </div>
                </form>
            )}
        </div>
    );
}

function RulePreview({ data, rule, draft, ruleName, onValidation }: {
    data: PrivacyRules; rule?: RuleItem; draft?: PrivacyRuleForm;
    ruleName: (rule: RuleItem) => string;
    onValidation?: (field: string, message: string) => void;
}) {
    const t = useTranslations('setting.privacyProtection');
    const errorMessage = usePrivacyError();
    const client = useQueryClient();
    const builtin = rule?.source === 'builtin' ? rule : undefined;
    const [inputType, setInputType] = useState<PreviewInputType>(builtin?.input_type ?? (draft?.kind === 'field_name' ? 'json' : 'text'));
    const [input, setInput] = useState(builtin?.example ?? '');
    const [mode, setMode] = useState<'current' | 'enabled'>('current');
    const [known, setKnown] = useState(builtin?.known_values?.join('\n') ?? '');
    const [result, setResult] = useState<{ signature: string; value: PrivacyPreviewResult } | null>(null);
    const [error, setError] = useState<{ signature: string; message: string } | null>(null);
    const [pending, setPending] = useState<{ signature: string; id: number } | null>(null);
    const abort = useRef<AbortController | null>(null);
    const sequence = useRef(0);
    const sampleId = useId();
    const showKnown = rule?.id === 'builtin.known_credentials' || (mode === 'enabled' && data.builtins.some((item) => item.id === 'builtin.known_credentials' && item.enabled));
    const request = { revision: data.revision, input_type: inputType, input, mode, rule_id: rule?.id, draft, known_values: showKnown ? known.split('\n').filter((value) => value !== '') : [] };
    const signature = JSON.stringify(request);
    useEffect(() => () => { abort.current?.abort(); sequence.current++; }, [signature]);
    const run = async () => {
        abort.current?.abort();
        const controller = new AbortController();
        abort.current = controller;
        const current = ++sequence.current;
        setPending({ signature, id: current });
        setError(null);
        try {
            const value = await previewPrivacy(request, controller.signal);
            if (current === sequence.current) setResult({ signature, value });
        } catch (error) {
            if (controller.signal.aborted || current !== sequence.current) return;
            const message = error instanceof ApiError && error.status === 409 ? t('conflict') : errorMessage(error, t('previewFailed'));
            setError({ signature, message });
            const validation = privacyValidation(error);
            if (validation && ['name', 'pattern', 'kind'].includes(validation.field)) onValidation?.(validation.field, message);
            if (error instanceof ApiError && error.status === 409) {
                await client.invalidateQueries({ queryKey: privacyRulesKey });
                const latest = client.getQueryData<PrivacyRules>(privacyRulesKey);
                if (latest) setError({ signature: JSON.stringify({ ...request, revision: latest.revision }), message });
            }
        } finally { setPending((latest) => latest?.id === current ? null : latest); }
    };
    const stale = result !== null && result.signature !== signature;
    const busy = pending?.signature === signature;
    const currentError = error?.signature === signature ? error.message : undefined;
    const hitName = (id: string) => {
        if ((id === 'custom_preview' || id === rule?.id) && draft) return draft.name || t('draftRule');
        const item = rulesOf(data).find((item) => item.id === id);
        return item ? ruleName(item) : id;
    };
    return (
        <section className="space-y-3 border-t border-border pt-4" aria-label={t('preview')}>
            <div><h4 className="text-sm font-semibold">{t('preview')}</h4><p className="mt-1 text-xs leading-relaxed text-muted-foreground">{t('previewHint')}</p></div>
            <div className="grid gap-3 sm:grid-cols-2">
                <label className="space-y-1.5 text-sm"><span>{t('previewMode')}</span><select className={selectClass} value={mode} onChange={(event) => setMode(event.target.value as typeof mode)}>
                    <option value="current">{t('currentRule')}</option><option value="enabled">{t('enabledRules')}</option>
                </select></label>
                <label className="space-y-1.5 text-sm"><span>{t('inputType')}</span><select className={selectClass} value={inputType} onChange={(event) => setInputType(event.target.value as PreviewInputType)}>
                    {(['text', 'json', 'url', 'headers'] as const).map((type) => <option key={type} value={type}>{t(`inputTypes.${type}`)}</option>)}
                </select></label>
            </div>
            {showKnown && <label className="block space-y-1.5 text-sm"><span>{t('knownValues')}</span><textarea className={`${textareaClass} font-mono`} rows={2} value={known} onChange={(event) => setKnown(event.target.value)} autoComplete="off" spellCheck={false} /><span className="block text-xs text-muted-foreground">{t('knownValuesHint', { count: data.limits.known_values })}</span></label>}
            <div className="space-y-1.5">
                <label htmlFor={sampleId} className="text-sm">{t('sample')}</label>
                <textarea id={sampleId} className={`${textareaClass} font-mono`} rows={4} value={input} onChange={(event) => setInput(event.target.value)} spellCheck={false} autoComplete="off" aria-invalid={!!currentError} aria-describedby={`${sampleId}-hint ${sampleId}-error`} />
                <p id={`${sampleId}-hint`} className="text-xs leading-relaxed text-muted-foreground">{inputType === 'headers' ? t('headersHint') : t('sampleLimit', { count: Math.floor(data.limits.preview_bytes / 1024) })}</p>
                <p id={`${sampleId}-error`} role={currentError ? 'alert' : undefined} className="text-xs text-destructive">{currentError}</p>
            </div>
            <Button type="button" variant="outline" disabled={busy} onClick={() => void run()}>{busy ? <LoaderCircle className="mr-2 size-4 animate-spin" /> : <Play className="mr-2 size-4" />}{t('runPreview')}</Button>
            <div aria-live="polite" className="space-y-2">
                {stale && <p role="status" className="text-xs text-muted-foreground">{t('previewStale')}</p>}
                {result && <div className={`space-y-2 ${stale ? 'opacity-50' : ''}`}>
                    <h5 className="text-xs font-medium">{t('output')}</h5>
                    <pre className="max-h-48 overflow-y-auto whitespace-pre-wrap break-all rounded-xl bg-muted/60 p-3 text-xs">{result.value.output || t('emptyOutput')}</pre>
                    <p className="text-xs font-medium">{t('matchedRules')}</p>
                    {result.value.hits.length ? <ul className="space-y-1 text-xs text-muted-foreground">{result.value.hits.map((hit) => <li key={hit.rule_id}>{t('hitCount', { name: hitName(hit.rule_id), count: hit.count })}</li>)}</ul> : <p className="text-xs text-muted-foreground">{t('noMatches')}</p>}
                </div>}
            </div>
        </section>
    );
}

function PrivacyDialogPanel() {
    const { setIsOpen, uniqueId } = useMorphingDialog();
    const t = useTranslations('setting.privacyProtection');
    const [editorOpen, setEditorOpen] = useState(false);
    return (
        <MorphingDialogContent dismissOnClickOutside={!editorOpen} suspendInteraction={editorOpen}>
            <p id={`motion-ui-morphing-dialog-description-${uniqueId}`} className="sr-only">{t('viewAll')}</p>
            <PrivacyPanel expanded titleId={`motion-ui-morphing-dialog-title-${uniqueId}`} onClose={() => setIsOpen(false)} onEditorChange={setEditorOpen} />
        </MorphingDialogContent>
    );
}

export function SettingPrivacy() { return <PrivacyPanel />; }
