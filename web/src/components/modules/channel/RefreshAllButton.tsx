import { useId } from 'react';
import { useIsMutating, useQueryClient } from '@tanstack/react-query';
import { RefreshCw } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { useChannelStats } from '@/api/channel';
import { IconButton } from '@/components/common/IconButton';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { refreshAllMutationKey, startChannelRefreshAll, useRefreshAllProgress } from './refresh-all';

export function RefreshAllButton() {
    const t = useTranslations('channel.refreshAll');
    const { data: channels, isError } = useChannelStats();
    const queryClient = useQueryClient();
    const progress = useRefreshAllProgress();
    const mutating = useIsMutating({ mutationKey: refreshAllMutationKey }) > 0;
    const pending = progress.pending || mutating;
    const hasEligible = channels?.some((channel) => channel.enabled && channel.follow_global_auto_update) ?? false;
    const noEligible = !pending && !isError && channels !== undefined && !hasEligible;
    const descriptionId = useId();
    const description = pending
        ? t('progress', { completed: progress.completed, total: progress.total })
        : noEligible ? t('noEnabled') : t('button');

    return (
        <Tooltip>
            <TooltipTrigger asChild>
                <span
                    className="inline-flex shrink-0 rounded-xl"
                    tabIndex={noEligible ? 0 : undefined}
                    role={noEligible ? 'group' : undefined}
                    aria-label={noEligible ? t('button') : undefined}
                    aria-describedby={noEligible ? descriptionId : undefined}
                >
                    <IconButton
                        aria-label={t('button')}
                        aria-busy={pending}
                        aria-describedby={descriptionId}
                        disabled={pending || isError || !hasEligible}
                        className="size-9 shrink-0 rounded-xl"
                        onClick={() => {
                            if (isError || !channels) return;
                            startChannelRefreshAll(queryClient, channels, t);
                        }}
                    >
                        <RefreshCw aria-hidden="true" className={`size-4 ${pending ? 'animate-spin' : ''}`} />
                    </IconButton>
                </span>
            </TooltipTrigger>
            <TooltipContent>{description}</TooltipContent>
            <span id={descriptionId} className="sr-only" role="status" aria-live="polite">{description}</span>
        </Tooltip>
    );
}
