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
    const hasEnabled = channels?.some((channel) => channel.enabled) ?? false;
    const noEnabled = !pending && !isError && channels !== undefined && !hasEnabled;
    const descriptionId = useId();
    const description = pending
        ? t('progress', { completed: progress.completed, total: progress.total })
        : noEnabled ? t('noEnabled') : t('button');

    return (
        <Tooltip>
            <TooltipTrigger asChild>
                <span
                    className="inline-flex shrink-0 rounded-xl"
                    tabIndex={noEnabled ? 0 : undefined}
                    role={noEnabled ? 'group' : undefined}
                    aria-label={noEnabled ? t('button') : undefined}
                    aria-describedby={noEnabled ? descriptionId : undefined}
                >
                    <IconButton
                        aria-label={t('button')}
                        aria-busy={pending}
                        aria-describedby={descriptionId}
                        disabled={pending || isError || !hasEnabled}
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
