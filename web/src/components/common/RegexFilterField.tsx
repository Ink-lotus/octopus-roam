import { useEffect, useId, useRef, useState, type ReactNode } from 'react';
import { useTranslations } from 'use-intl';
import { LoaderCircle, Pencil, Plus, X } from 'lucide-react';
import { Dialog } from 'radix-ui';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import {
    compileModelFilter,
    parseModelFilter,
    type ModelFilterAction,
    type ModelFilterJoin,
    type ModelFilterOperator,
    type ModelFilterRule,
} from '@/lib/model-filter';
import { isValidRegex } from '@/lib/regex';

interface RegexFilterFieldProps {
    id: string;
    label: string;
    labelIcon?: ReactNode;
    value: string;
    validationValue?: string;
    placeholder?: string;
    onChange: (value: string) => void | Promise<void>;
    onOpenChange?: (open: boolean) => void;
    saveImmediately?: boolean;
}

interface DraftRule extends ModelFilterRule {
    id: number;
}

interface FilterDraft {
    rules: DraftRule[];
    nextId: number;
    rawPattern: string;
    source: 'visual' | 'raw';
    editingExpression: boolean;
    focusRuleId: number | null;
}

function emptyRule(id: number, join: ModelFilterJoin): DraftRule {
    return { id, action: 'keep', operator: 'contains', value: '', join };
}

function parseVisualRules(value: string): ModelFilterRule[] | null {
    const rules = parseModelFilter(value);
    // A single-line input strips CR/LF, so keep these values in their original expression.
    return rules?.some(rule => rule.value.includes('\r') || rule.value.includes('\n')) ? null : rules;
}

function createDraft(value: string): FilterDraft {
    const restored = parseVisualRules(value);
    const rules = value === '' ? [emptyRule(0, 'and')] : (restored ?? []).map((rule, id) => ({ ...rule, id }));
    return {
        rules,
        nextId: rules.length,
        rawPattern: value,
        source: restored === null ? 'raw' : 'visual',
        editingExpression: false,
        focusRuleId: rules[0]?.id ?? null,
    };
}

export function RegexFilterField({
    id, label, labelIcon, value, validationValue, placeholder, onChange, onOpenChange, saveImmediately = false,
}: RegexFilterFieldProps) {
    const t = useTranslations('regexFilter');
    const [open, setOpen] = useState(false);
    const [isApplying, setIsApplying] = useState(false);
    const applyPending = useRef(false);
    const isValid = isValidRegex(validationValue ?? value);
    const errorId = `${id}-error`;

    useEffect(() => () => onOpenChange?.(false), [onOpenChange]);

    const changeOpen = (next: boolean) => {
        if (applyPending.current) return;
        setOpen(next);
        onOpenChange?.(next);
    };

    const applyPattern = async (pattern: string) => {
        if (applyPending.current) return;
        applyPending.current = true;
        setIsApplying(true);
        try {
            await onChange(pattern);
            setOpen(false);
            onOpenChange?.(false);
        } finally {
            applyPending.current = false;
            setIsApplying(false);
        }
    };

    return (
        <Dialog.Root open={open} onOpenChange={changeOpen}>
            <div className="min-w-0 space-y-3">
                <div className="flex items-center justify-between gap-3">
                    <div className="flex min-w-0 items-center gap-2 text-sm font-medium">
                        {labelIcon}
                        <span>{label}</span>
                    </div>
                    <Dialog.Trigger asChild>
                        <Button id={id} type="button" variant="outline" size="sm" disabled={isApplying}
                            aria-label={`${label}: ${t(value ? 'edit' : 'add')}`}
                            aria-invalid={!isValid} aria-describedby={!isValid ? errorId : undefined}
                            className="shrink-0 rounded-xl">
                            {value ? <Pencil className="size-3.5" /> : <Plus className="size-3.5" />}
                            {t(value ? 'edit' : 'add')}
                        </Button>
                    </Dialog.Trigger>
                </div>
                <pre role="region" aria-label={`${label}: ${t('preview')}`} tabIndex={value ? 0 : undefined}
                    className="h-36 min-w-0 overflow-y-auto overscroll-contain whitespace-pre-wrap break-all rounded-xl border border-border px-3 py-3 font-mono text-xs leading-relaxed text-muted-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring">
                    {value || t('notSet')}
                </pre>
                <p className="text-xs text-muted-foreground">{t('combined')}</p>
                {!isValid && <p id={errorId} role="alert" className="text-sm text-destructive">{t('invalid')}</p>}
            </div>
            {open && (
                <Dialog.Portal>
                    <Dialog.Overlay className="fixed inset-0 z-50 bg-black/60 backdrop-blur-sm" />
                    <Dialog.Content
                        className="fixed left-1/2 top-1/2 z-50 max-h-[calc(100dvh-2rem)] w-[calc(100vw-2rem)] max-w-[56rem] -translate-x-1/2 -translate-y-1/2 overflow-y-auto overscroll-contain rounded-3xl border border-border bg-card p-5 text-card-foreground shadow-xl outline-none sm:p-6"
                        onEscapeKeyDown={(event) => { event.stopPropagation(); if (isApplying) event.preventDefault(); }}
                        onPointerDownOutside={(event) => event.preventDefault()}>
                        <ModelFilterEditor label={label} value={value} placeholder={placeholder}
                            onApply={applyPattern} onCancel={() => changeOpen(false)} isApplying={isApplying}
                            applyLabel={t(saveImmediately ? 'save' : 'apply')} />
                    </Dialog.Content>
                </Dialog.Portal>
            )}
        </Dialog.Root>
    );
}

function ModelFilterEditor({ label, value, placeholder, onApply, onCancel, isApplying, applyLabel }: {
    label: string;
    value: string;
    placeholder?: string;
    onApply: (pattern: string) => Promise<void>;
    onCancel: () => void;
    isApplying: boolean;
    applyLabel: string;
}) {
    const t = useTranslations('regexFilter');
    const editorId = useId();
    const [draft, setDraft] = useState(() => createDraft(value));
    const [applyError, setApplyError] = useState<string | null>(null);
    const formRef = useRef<HTMLFormElement>(null);
    const rawRef = useRef<HTMLTextAreaElement>(null);
    const errorId = `${editorId}-error`;

    useEffect(() => {
        if (draft.focusRuleId !== null) {
            formRef.current?.querySelector<HTMLInputElement>(`[data-filter-rule-value="${draft.focusRuleId}"]`)?.focus();
        }
    }, [draft.focusRuleId]);
    useEffect(() => { if (draft.editingExpression) rawRef.current?.focus(); }, [draft.editingExpression]);

    let pattern = draft.rawPattern;
    let incomplete = false;
    if (draft.source === 'visual') {
        try { pattern = compileModelFilter(draft.rules); }
        catch { incomplete = true; }
    }
    const valid = !incomplete && isValidRegex(pattern);
    const error = incomplete ? t('emptyCondition') : !valid ? t('invalid') : applyError;
    const canEditExpression = !(draft.source === 'visual' && incomplete && draft.rules.some(rule => rule.value !== ''));

    const updateDraft = (update: (previous: FilterDraft) => FilterDraft) => {
        setApplyError(null);
        setDraft(update);
    };
    const changeRule = (id: number, patch: Partial<ModelFilterRule>) => updateDraft(previous => ({
        ...previous, rules: previous.rules.map(rule => rule.id === id ? { ...rule, ...patch } : rule),
    }));
    const addRule = (join: ModelFilterJoin) => updateDraft(previous => ({
        ...previous,
        rules: [...previous.rules, emptyRule(previous.nextId, join)],
        nextId: previous.nextId + 1,
        focusRuleId: previous.nextId,
    }));
    const removeRule = (id: number) => updateDraft(previous => {
        const index = previous.rules.findIndex(rule => rule.id === id);
        const rules = previous.rules.filter(rule => rule.id !== id).map((rule, nextIndex) => nextIndex === 0 ? { ...rule, join: 'and' as const } : rule);
        return { ...previous, rules, focusRuleId: rules[Math.max(0, index - 1)]?.id ?? null };
    });
    const clearToVisual = () => updateDraft(() => createDraft(''));
    const toggleExpressionEditing = () => updateDraft(previous => {
        if (!previous.editingExpression) {
            const rawPattern = previous.source === 'visual'
                ? (previous.rules.every(rule => rule.value === '') ? '' : compileModelFilter(previous.rules))
                : previous.rawPattern;
            return { ...previous, rawPattern, source: 'raw', editingExpression: true, focusRuleId: null };
        }
        const restored = parseVisualRules(previous.rawPattern);
        if (restored === null) return { ...previous, editingExpression: false };
        return {
            ...previous,
            rules: restored.map((rule, index) => ({ ...rule, id: previous.nextId + index })),
            nextId: previous.nextId + restored.length,
            source: 'visual',
            editingExpression: false,
            focusRuleId: null,
        };
    });
    const apply = async () => {
        if (!valid || isApplying) return;
        setApplyError(null);
        try { await onApply(pattern); }
        catch (failure) {
            setApplyError(`${t('applyFailed')}${failure instanceof Error ? ` ${failure.message}` : ''}`);
        }
    };

    return (
        <form ref={formRef} className="space-y-5" onSubmit={(event) => { event.preventDefault(); event.stopPropagation(); void apply(); }}>
            <div className="flex items-center justify-between gap-3">
                <Dialog.Title className="text-base font-semibold">{label}</Dialog.Title>
                <Button type="button" variant="ghost" size="icon" disabled={isApplying} onClick={onCancel}
                    aria-label={t('cancel')} className="shrink-0 rounded-xl"><X className="size-4" /></Button>
            </div>
            <Dialog.Description className="text-sm text-muted-foreground">{t('description')}</Dialog.Description>

            {draft.source === 'visual' && (
                <div className="space-y-3">
                    <div aria-hidden="true" className="hidden grid-cols-[94px_144px_minmax(0,1fr)_110px_36px] gap-2 text-xs text-muted-foreground sm:grid">
                        <span>{t('action')}</span><span>{t('operator')}</span><span>{t('value')}</span><span>{t('append')}</span><span />
                    </div>
                    {draft.rules.map((rule, index) => (
                        <div key={rule.id} className="space-y-2">
                            {index > 0 && (
                                <div className={rule.join === 'or' ? 'flex items-center gap-3 py-1' : 'pl-5'}>
                                    {rule.join === 'or' && <span aria-hidden="true" className="h-px flex-1 bg-border" />}
                                    <Button type="button" variant="outline" size="xs" disabled={isApplying}
                                        onClick={() => changeRule(rule.id, { join: rule.join === 'and' ? 'or' : 'and' })}
                                        aria-label={t('changeJoin', { number: index + 1, join: rule.join.toUpperCase() })}
                                        className={`rounded-md bg-transparent text-xs ${rule.join === 'or' ? 'text-primary' : 'text-muted-foreground'}`}>
                                        {rule.join.toUpperCase()}
                                    </Button>
                                    {rule.join === 'or' && <span aria-hidden="true" className="h-px flex-1 bg-border" />}
                                </div>
                            )}
                            <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)_36px] items-center gap-2 rounded-xl border border-border p-2.5 sm:grid-cols-[94px_144px_minmax(0,1fr)_110px_36px] sm:rounded-none sm:border-0 sm:p-0">
                                <Select value={rule.action} disabled={isApplying} onValueChange={(action) => changeRule(rule.id, { action: action as ModelFilterAction })}>
                                    <SelectTrigger aria-label={t('conditionAction', { number: index + 1 })} className="w-full rounded-xl"><SelectValue /></SelectTrigger>
                                    <SelectContent><SelectItem value="keep">{t('actions.keep')}</SelectItem><SelectItem value="exclude">{t('actions.exclude')}</SelectItem></SelectContent>
                                </Select>
                                <Select value={rule.operator} disabled={isApplying} onValueChange={(operator) => changeRule(rule.id, { operator: operator as ModelFilterOperator })}>
                                    <SelectTrigger aria-label={t('conditionOperator', { number: index + 1 })} className="w-full rounded-xl"><SelectValue /></SelectTrigger>
                                    <SelectContent>{(['equals', 'starts', 'ends', 'contains'] as const).map(operator => <SelectItem key={operator} value={operator}>{t(`operators.${operator}`)}</SelectItem>)}</SelectContent>
                                </Select>
                                <Input value={rule.value} disabled={isApplying} data-filter-rule-value={rule.id}
                                    onChange={(event) => changeRule(rule.id, { value: event.target.value })}
                                    aria-label={t('conditionValue', { number: index + 1 })} aria-invalid={rule.value === ''}
                                    aria-describedby={rule.value === '' ? errorId : undefined} placeholder={t('valuePlaceholder')}
                                    className="col-span-2 row-start-2 min-w-0 rounded-xl sm:col-span-1 sm:col-start-3 sm:row-start-1" />
                                {index === draft.rules.length - 1 ? (
                                    <div className="col-span-2 row-start-3 flex gap-1.5 sm:col-span-1 sm:col-start-4 sm:row-start-1">
                                        {(['and', 'or'] as const).map(join => <Button key={join} type="button" variant="outline" disabled={isApplying}
                                            aria-label={t('appendCondition', { join: join.toUpperCase() })} onClick={() => addRule(join)}
                                            className="min-w-0 flex-1 rounded-xl px-2">{join.toUpperCase()}</Button>)}
                                    </div>
                                ) : <span aria-hidden="true" className="hidden sm:block" />}
                                <Button type="button" variant="ghost" size="icon" disabled={isApplying}
                                    aria-label={t('removeCondition', { number: index + 1 })} onClick={() => removeRule(rule.id)}
                                    className="col-start-3 row-start-1 shrink-0 rounded-xl sm:col-start-5"><X className="size-4" /></Button>
                            </div>
                        </div>
                    ))}
                    {draft.rules.length === 0 && <Button type="button" variant="outline" disabled={isApplying} onClick={() => addRule('and')} className="rounded-xl"><Plus className="size-4" />{t('addCondition')}</Button>}
                    <p className="text-xs leading-relaxed text-muted-foreground">{t('combinationHint')}</p>
                </div>
            )}

            <div className="space-y-2">
                <div className="flex items-center justify-between gap-3 text-sm">
                    <span>{t(draft.editingExpression ? 'editExpression' : 'preview')}</span>
                    <Button type="button" variant="link" disabled={isApplying || !canEditExpression} onClick={toggleExpressionEditing}
                        className="h-auto px-0 py-1 text-xs">{t(draft.editingExpression ? 'finishEditing' : 'editExpression')}</Button>
                </div>
                {draft.editingExpression ? (
                    <div className="space-y-2">
                        <label htmlFor={`${editorId}-raw`} className="text-xs text-muted-foreground">{t('syntax')}</label>
                        <textarea id={`${editorId}-raw`} ref={rawRef} value={draft.rawPattern} disabled={isApplying}
                            onChange={(event) => updateDraft(previous => ({ ...previous, rawPattern: event.target.value }))}
                            aria-label={t('editExpression')} aria-invalid={!valid} aria-describedby={!valid ? errorId : undefined}
                            spellCheck={false} placeholder={placeholder ?? t('rawPlaceholder')}
                            className="min-h-32 w-full resize-y rounded-xl border border-border bg-background px-3 py-3 font-mono text-xs leading-relaxed focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" />
                    </div>
                ) : (
                    <pre aria-label={t('preview')} className="min-h-16 cursor-default whitespace-pre-wrap break-all rounded-xl border border-border bg-background px-4 py-3 font-mono text-xs leading-relaxed text-muted-foreground">
                        {incomplete ? t('incompletePreview') : pattern || t('emptyPreview')}
                    </pre>
                )}
                {draft.source === 'raw' && parseVisualRules(draft.rawPattern) === null && (
                    <p className="text-xs text-muted-foreground">
                        {t('customHint')}{' '}
                        <Button type="button" variant="link" disabled={isApplying} onClick={clearToVisual}
                            className="h-auto px-0 py-1 text-xs">{t('clearToVisual')}</Button>
                    </p>
                )}
            </div>
            {error && <p id={errorId} role="alert" className="text-sm text-destructive">{error}</p>}
            <div className="flex items-center justify-end gap-2 pt-1">
                <Button type="button" variant="outline" disabled={isApplying} onClick={onCancel} className="rounded-xl">{t('cancel')}</Button>
                <Button type="submit" disabled={!valid || isApplying} className="rounded-xl">
                    {isApplying && <LoaderCircle className="size-4 animate-spin" />}{isApplying ? t('applying') : applyLabel}
                </Button>
            </div>
        </form>
    );
}
