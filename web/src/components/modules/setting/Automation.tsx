import { useId, useState } from 'react';
import { RefreshCw } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { toast } from 'sonner';
import { SettingKey, useSettingList, useSetSetting } from '@/api/setting';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';

export function SettingAutomation() {
    const t = useTranslations('automation');
    const settings = useSettingList();
    const save = useSetSetting();
    const id = useId();
    const [intervalDraft, setIntervalDraft] = useState<string | null>(null);
    const value = (key: string, fallback: string) => settings.data?.find((item) => item.key === key)?.value ?? fallback;
    const savedInterval = value(SettingKey.ChannelAutoUpdateInterval, '1440');
    const interval = intervalDraft ?? savedInterval;
    const valid = /^\d+$/.test(interval) && Number(interval) >= 1 && Number(interval) <= 10080;
    const disabled = !settings.data || settings.isError || save.isPending;
    const update = (key: string, next: string) => save.mutate({ key, value: next }, {
        onSuccess: () => {
            if (key === SettingKey.ChannelAutoUpdateInterval) setIntervalDraft(null);
            toast.success(t('saved'));
        },
        onError: (error) => toast.error(t('saveFailed'), { description: error.message }),
    });

    return (
        <section className="space-y-5 rounded-3xl border border-border bg-card p-6" aria-busy={save.isPending}>
            <h2 className="flex items-center gap-2 text-lg font-semibold"><RefreshCw className="size-5" />{t('title')}</h2>
            <p className="text-xs leading-relaxed text-muted-foreground">{t('description')}</p>
            {settings.isError && <p role="alert" className="text-sm text-destructive">{t('loadFailed')}</p>}
            <div className="flex items-center justify-between gap-3">
                <Label htmlFor={`${id}-enabled`}>{t('enabled')}</Label>
                <Switch id={`${id}-enabled`} checked={value(SettingKey.ChannelAutoUpdateEnabled, 'false') === 'true'} disabled={disabled}
                    onCheckedChange={(checked) => update(SettingKey.ChannelAutoUpdateEnabled, String(checked))} />
            </div>
            <form className="space-y-2" onSubmit={(event) => {
                event.preventDefault();
                if (valid && !disabled) update(SettingKey.ChannelAutoUpdateInterval, String(Number(interval)));
            }}>
                <Label htmlFor={`${id}-interval`}>{t('interval')}</Label>
                <div className="flex gap-2">
                    <Input id={`${id}-interval`} type="number" min={1} max={10080} step={1} value={interval}
                        onChange={(event) => setIntervalDraft(event.target.value)} disabled={disabled}
                        aria-invalid={!valid} aria-describedby={`${id}-interval-hint`} className="rounded-xl" />
                    <Button type="submit" disabled={disabled || !valid || interval === savedInterval}>{t('save')}</Button>
                </div>
                <p id={`${id}-interval-hint`} className="text-xs text-muted-foreground">{t('intervalHint')}</p>
            </form>
            <div className="space-y-2">
                <div className="flex items-center justify-between gap-3">
                    <Label htmlFor={`${id}-groups`}>{t('autoAdd')}</Label>
                    <Switch id={`${id}-groups`} checked={value(SettingKey.ChannelAutoAddGroups, 'false') === 'true'} disabled={disabled}
                        onCheckedChange={(checked) => update(SettingKey.ChannelAutoAddGroups, String(checked))}
                        aria-describedby={`${id}-groups-hint`} />
                </div>
                <p id={`${id}-groups-hint`} className="text-xs text-muted-foreground">{t('autoAddHint')}</p>
            </div>
        </section>
    );
}
