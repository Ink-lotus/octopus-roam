import { useId } from 'react';
import { useTranslations } from 'use-intl';
import type { GroupMode, GroupRelayConfig, RetryMode } from '@/api/group';
import { Field, FieldLabel } from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';

type NumericField = Exclude<keyof GroupRelayConfig, 'retry_mode' | 'member_affinity_seconds'>;

// 不传 mode 时编辑两种模式的全局默认值; 单组只展示当前模式的有效字段。
export function RelayConfigFields({ value, onChange, mode, disabled = false }: {
    value: GroupRelayConfig;
    onChange: (config: GroupRelayConfig) => void;
    mode?: GroupMode;
    disabled?: boolean;
}) {
    const t = useTranslations('group.form');
    const id = useId();
    const numberField = (field: NumericField, label: string, hint: string, min = 1, max?: number) => (
        <Field>
            <FieldLabel htmlFor={`${id}-${field}`}>{label}</FieldLabel>
            <Input
                id={`${id}-${field}`}
                aria-describedby={`${id}-${field}-hint`}
                type="number"
                inputMode="numeric"
                min={min}
                max={max}
                step={1}
                required
                value={value[field]}
                onChange={(event) => {
                    const parsed = Number.parseInt(event.target.value, 10);
                    const next = Number.isFinite(parsed) ? Math.max(min, parsed) : min;
                    onChange({ ...value, [field]: max === undefined ? next : Math.min(next, max), member_affinity_seconds: 0 });
                }}
                className="rounded-xl"
            />
            <p id={`${id}-${field}-hint`} className="text-xs leading-relaxed text-muted-foreground">{hint}</p>
        </Field>
    );

    return (
        <div className="space-y-6">
            <fieldset disabled={disabled} className="space-y-4 disabled:opacity-60">
                <legend className="mb-3 text-sm font-semibold">{t('commonSettings')}</legend>
                <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
                    {numberField('member_non_stream_response_timeout_seconds', t('nonStreamTimeout'), t('nonStreamTimeoutHint'))}
                    {numberField('member_stream_first_event_timeout_seconds', t('streamTimeout'), t('streamTimeoutHint'))}
                    <Field>
                        <FieldLabel htmlFor={`${id}-retry-mode`}>{t('retryMode')}</FieldLabel>
                        <Select
                            value={value.retry_mode}
                            disabled={disabled}
                            onValueChange={(retryMode) => onChange({ ...value, retry_mode: retryMode as RetryMode, member_affinity_seconds: 0 })}
                        >
                            <SelectTrigger id={`${id}-retry-mode`} aria-describedby={`${id}-retry-mode-hint`} className="w-full rounded-xl">
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value="fixed">{t('retryModeFixed')}</SelectItem>
                                <SelectItem value="exponential">{t('retryModeExponential')}</SelectItem>
                            </SelectContent>
                        </Select>
                        <p id={`${id}-retry-mode-hint`} className="text-xs leading-relaxed text-muted-foreground">{t('retryModeHint')}</p>
                    </Field>
                    {value.retry_mode === 'fixed'
                        ? numberField('member_retry_interval_seconds', t('retryInterval'), t('retryIntervalHint'))
                        : <>
                            {numberField('retry_initial_interval_seconds', t('retryInitialInterval'), t('retryInitialIntervalHint'), 1, Number.MAX_SAFE_INTEGER)}
                            {numberField('retry_max_interval_seconds', t('retryMaxInterval'), t('retryMaxIntervalHint'), 1, Number.MAX_SAFE_INTEGER)}
                        </>}
                </div>
            </fieldset>
            {mode !== 'failover' && (
                <fieldset disabled={disabled} className="space-y-4 disabled:opacity-60">
                    <legend className="mb-3 text-sm font-semibold">{t('manualSettings')}</legend>
                    {numberField('manual_max_attempts', t('manualMaxAttempts'), t('manualMaxAttemptsHint'), 0)}
                </fieldset>
            )}
            {mode !== 'manual' && (
                <fieldset disabled={disabled} className="space-y-4 disabled:opacity-60">
                    <legend className="mb-3 text-sm font-semibold">{t('failoverSettings')}</legend>
                    <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
                        {numberField('member_max_attempts', t('retryCount'), t('retryCountHint'))}
                        {numberField('member_cooldown_seconds', t('cooldown'), t('cooldownHint'))}
                    </div>
                </fieldset>
            )}
        </div>
    );
}
