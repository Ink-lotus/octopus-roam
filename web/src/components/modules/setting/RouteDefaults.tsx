import { useId, useState, type FormEvent } from 'react';
import { Route } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { toast } from 'sonner';
import { useGroupRouteDefaults, useSaveGroupRouteDefaults, type GroupRelayConfig } from '@/api/group';
import { RelayConfigFields } from '@/components/modules/group/RelayConfigFields';
import { Button } from '@/components/ui/button';

export function SettingRouteDefaults() {
    const t = useTranslations('setting.routeDefaults');
    const defaults = useGroupRouteDefaults();
    const save = useSaveGroupRouteDefaults();
    const [draft, setDraft] = useState<GroupRelayConfig | null>(null);
    const [applyToExisting, setApplyToExisting] = useState(false);
    const id = useId();
    const config = draft ?? defaults.data;

    const submit = async (event: FormEvent<HTMLFormElement>) => {
        event.preventDefault();
        if (!config || save.isPending || defaults.isFetching || defaults.isError) return;
        try {
            const result = await save.mutateAsync({ relay_config: config, apply_to_existing: applyToExisting });
            setDraft(null);
            setApplyToExisting(false);
            toast.success(applyToExisting ? t('applied', { count: result.applied_groups }) : t('saved'));
        } catch (error) {
            toast.error(t('saveFailed'), { description: error instanceof Error ? error.message : undefined });
        }
    };

    return (
        <section className="space-y-5 rounded-3xl border border-border bg-card p-6">
            <h2 className="flex items-center gap-2 text-lg font-bold text-card-foreground">
                <Route className="size-5" aria-hidden="true" />
                {t('title')}
            </h2>
            <p className="text-sm leading-relaxed text-muted-foreground">{t('description')}</p>
            {defaults.isPending && <p role="status" className="text-sm text-muted-foreground">{t('loading')}</p>}
            {defaults.isError && (
                <div className="space-y-2">
                    <p role="alert" className="text-sm text-destructive">{t('loadFailed')}</p>
                    <Button type="button" variant="outline" onClick={() => void defaults.refetch()} disabled={defaults.isFetching || save.isPending}>
                        {t('retry')}
                    </Button>
                </div>
            )}
            {config && (
                <form onSubmit={submit} className="space-y-5" aria-busy={save.isPending}>
                    <RelayConfigFields value={config} onChange={setDraft} disabled={save.isPending} />
                    <div className="space-y-2 rounded-xl border border-border bg-muted/30 p-3">
                        <label htmlFor={`${id}-apply`} className="flex cursor-pointer items-start gap-2 text-sm font-medium">
                            <input
                                id={`${id}-apply`}
                                type="checkbox"
                                checked={applyToExisting}
                                onChange={(event) => setApplyToExisting(event.target.checked)}
                                disabled={save.isPending}
                                aria-describedby={`${id}-apply-hint`}
                                className="mt-0.5 size-4 shrink-0 accent-primary focus-visible:outline-2 focus-visible:outline-ring"
                            />
                            {t('applyToExisting')}
                        </label>
                        <p id={`${id}-apply-hint`} className="text-xs leading-relaxed text-muted-foreground">{t('applyHint')}</p>
                    </div>
                    {save.isError && <p role="alert" className="text-sm text-destructive">{t('saveFailed')}</p>}
                    <Button type="submit" className="w-full rounded-xl" disabled={save.isPending || defaults.isFetching || defaults.isError}>
                        {save.isPending ? t('saving') : t('save')}
                    </Button>
                </form>
            )}
        </section>
    );
}
