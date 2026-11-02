import { useTranslations } from 'use-intl';
import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';
import type { CooldownHit } from '@/api/cooldown';

interface CooldownHitListProps {
    hits?: CooldownHit[];
    now: number;
    compact?: boolean;
    className?: string;
}

export function CooldownHitList({ hits = [], now, compact = false, className }: CooldownHitListProps) {
    const t = useTranslations('cooldown');
    const activeHits = hits.filter((hit) => hit.until > now);
    if (activeHits.length === 0) return null;

    return (
        <ul aria-label={t('listLabel')} className={cn('flex min-w-0 flex-wrap items-center gap-1', className)}>
            {activeHits.map((hit) => {
                const seconds = Math.max(0, Math.ceil((hit.until - now) / 1000));
                const scope = hit.scope === 'channel' ? t('channelScope') : t('modelScope');
                const target = hit.scope === 'channel' ? t('allModels') : hit.model_name || t('unknownModel');
                const conditions = [
                    hit.rule.error_code && `${t('errorCode')}: ${hit.rule.error_code}`,
                    hit.rule.keyword && `${t('keyword')}: ${hit.rule.keyword}`,
                ].filter(Boolean).join(' + ');
                const until = new Date(hit.until).toLocaleString();
                const label = compact
                    ? t('compactSeconds', { seconds })
                    : t('hit', { scope, target, seconds });
                const title = t('details', {
                    scope,
                    target,
                    conditions,
                    ruleIndex: hit.rule_index + 1,
                    seconds,
                    until,
                });
                return (
                    <li key={`${hit.channel_id}:${hit.rule_index}:${hit.model_name ?? ''}:${hit.until}`}>
                        <Badge
                            variant="outline"
                            aria-label={label}
                            title={title}
                            className="shrink-0 border-sky-500/30 bg-sky-500/10 px-1.5 py-0 text-[10px] font-medium text-sky-700 dark:text-sky-300"
                        >
                            {label}
                        </Badge>
                    </li>
                );
            })}
        </ul>
    );
}
