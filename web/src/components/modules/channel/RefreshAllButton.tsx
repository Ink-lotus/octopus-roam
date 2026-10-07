import { useIsMutating, useMutation, useQueryClient } from '@tanstack/react-query';
import { RefreshCw } from 'lucide-react';
import { toast } from 'sonner';
import { useTranslations } from 'use-intl';
import { type ChannelStatsFormatted, useChannelStats, useRefreshChannelModels } from '@/api/channel';
import { channelStatsQueryOptions } from '@/api/queries';
import { IconButton } from '@/components/common/IconButton';
import { useChannelPreferences } from '@/stores/channel';

const mutationKey = ['channels', 'refresh-all'];
const toastId = 'channel-refresh-all';

export function RefreshAllButton() {
    const t = useTranslations('channel.refreshAll');
    const { data: channels, isError } = useChannelStats();
    const queryClient = useQueryClient();
    const refreshChannel = useRefreshChannelModels();
    // 从共享 mutation 缓存读取状态，切换页面再返回时仍防止重复提交。
    const pending = useIsMutating({ mutationKey }) > 0;
    const refreshAll = useMutation({
        mutationKey,
        retry: false,
        mutationFn: async (targets: ChannelStatsFormatted[]) => {
            const preferences = useChannelPreferences.getState().autoAddGroups;
            const summary = { success: 0, failed: 0, skipped: 0, warnings: 0, empty: 0 };
            const failures: string[] = [];
            let completed = 0;
            const showProgress = () => toast.loading(
                t('progress', { completed, total: targets.length }),
                { id: toastId },
            );
            showProgress();

            // 逐个复用单渠道刷新流程，避免同时向所有上游发出请求。
            for (const channel of targets) {
                const latest = queryClient.getQueryData(channelStatsQueryOptions.queryKey)
                    ?.find((item) => item.channel_id === channel.channel_id) ?? channel;
                if (latest.refresh?.running) {
                    summary.skipped++;
                } else {
                    try {
                        const result = await refreshChannel.mutateAsync({
                            id: channel.channel_id,
                            autoAddGroups: preferences[channel.channel_id] ?? true,
                        });
                        if (result.success) {
                            summary.success++;
                            if (result.zero_models) summary.empty++;
                            if (result.warnings?.length || result.error) summary.warnings++;
                        } else {
                            summary.failed++;
                            failures.push(`${channel.channel_name}: ${result.error || t('channelFailed')}`);
                        }
                    } catch (error) {
                        // 单个渠道的 HTTP 或网络错误不应中断其余渠道。
                        summary.failed++;
                        failures.push(`${channel.channel_name}: ${error instanceof Error ? error.message : t('channelFailed')}`);
                    }
                }
                completed++;
                showProgress();
            }
            return { summary, failures };
        },
        onSuccess: ({ summary, failures }) => {
            const description = [t('summary', summary)];
            description.push(...failures.slice(0, 3));
            if (summary.empty) description.push(t('empty', { count: summary.empty }));
            if (summary.warnings) description.push(t('warnings', { count: summary.warnings }));
            if (summary.failed || summary.warnings) description.push(t('details'));
            if (summary.skipped) description.push(t('skipped'));
            const options = { id: toastId, description: description.join('\n'), duration: 8000 };
            if (summary.failed && !summary.success) {
                toast.error(t('finished'), options);
            } else if (summary.failed || summary.warnings || summary.skipped) {
                toast.warning(t('finished'), options);
            } else {
                toast.success(t('finished'), options);
            }
        },
        onError: (error) => {
            toast.error(t('failed'), { id: toastId, description: error.message });
        },
    });

    return (
        <IconButton
            aria-label={t('button')}
            aria-busy={pending}
            tip={pending ? t('pending') : t('button')}
            disabled={pending || isError || !channels?.length}
            className="size-9 shrink-0 rounded-xl"
            onClick={() => {
                if (queryClient.isMutating({ mutationKey }) || isError || !channels?.length) return;
                refreshAll.mutate(channels);
            }}
        >
            <RefreshCw aria-hidden="true" className={`size-4 ${pending ? 'animate-spin' : ''}`} />
        </IconButton>
    );
}
