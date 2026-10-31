import type { QueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { create } from 'zustand';
import { channelModelRefreshMutationOptions, invalidateChannelModelRefreshQueries, type ChannelModelRefreshSkipReason, type ChannelStats } from '@/api/channel';
import { channelStatsQueryOptions } from '@/api/queries';
import { useChannelPreferences } from '@/stores/channel';

export const refreshAllMutationKey = ['channels', 'refresh-all'] as const;
const toastId = 'channel-refresh-all';

type RefreshAllTarget = Pick<ChannelStats, 'channel_id' | 'channel_name' | 'enabled'>;
type RefreshAllMessage = 'progress' | 'summary' | 'channelFailed' | 'empty' | 'warnings' | 'details' | 'skipped' | 'finished' | 'failed';
type Translate = (key: RefreshAllMessage, values?: Record<string, number>) => string;

// 进程内共享快照，不持久化进行中的任务；页面卸载不会销毁异步循环或进度。
export const useRefreshAllProgress = create<{ pending: boolean; completed: number; total: number }>()(() => ({
    pending: false,
    completed: 0,
    total: 0,
}));

export function startChannelRefreshAll(queryClient: QueryClient, channels: readonly RefreshAllTarget[], t: Translate) {
    if (queryClient.isMutating({ mutationKey: refreshAllMutationKey }) || useRefreshAllProgress.getState().pending) return;
    const targets = channels.filter((channel) => channel.enabled);
    if (!targets.length) return;

    // 在首次 await 之前占用任务，阻止两入口或快速重复点击同时创建第二轮。
    useRefreshAllProgress.setState({ pending: true, completed: 0, total: targets.length });
    const task = queryClient.getMutationCache().build(queryClient, {
        mutationKey: refreshAllMutationKey,
        retry: false,
        mutationFn: async (targets: RefreshAllTarget[]) => {
            const summary = { success: 0, failed: 0, skipped: 0, warnings: 0, empty: 0 };
            const skipReasons: Record<ChannelModelRefreshSkipReason, number> = { disabled: 0, deleted: 0, in_progress: 0 };
            const failures: string[] = [];
            const skip = (reason?: ChannelModelRefreshSkipReason) => {
                summary.skipped++;
                if (reason) skipReasons[reason]++;
            };
            const showProgress = () => {
                const completed = summary.success + summary.failed + summary.skipped;
                useRefreshAllProgress.setState({ completed });
                toast.loading(t('progress', { completed, total: targets.length }), { id: toastId });
            };
            showProgress();

            for (const channel of targets) {
                let name = channel.channel_name;
                try {
                    // 强制重取全部渠道；缺项表示已删除，绝不回退到初始旧对象。
                    const latestChannels = await queryClient.fetchQuery({ ...channelStatsQueryOptions, staleTime: 0, retry: false });
                    const latest = latestChannels.find((item) => item.channel_id === channel.channel_id);
                    if (!latest) {
                        skip('deleted');
                    } else if (!latest.enabled) {
                        skip('disabled');
                    } else if (latest.refresh?.running) {
                        skip('in_progress');
                    } else {
                        name = latest.channel_name;
                        const result = await queryClient.getMutationCache()
                            .build(queryClient, channelModelRefreshMutationOptions(queryClient))
                            .execute({
                                id: latest.channel_id,
                                autoAddGroups: useChannelPreferences.getState().autoAddGroups[latest.channel_id] ?? true,
                                enabledOnly: true,
                            });
                        if (result.skipped) {
                            skip(result.skip_reason);
                        } else if (result.success) {
                            summary.success++;
                            if (result.zero_models) summary.empty++;
                            if (result.warnings?.length || result.error || result.key_results.some((key) => key.error)) summary.warnings++;
                        } else {
                            summary.failed++;
                            failures.push(`${name}: ${result.error || t('channelFailed')}`);
                        }
                    }
                } catch (error) {
                    // 最新状态查询或单渠道请求失败时，继续其余目标，不发送旧缓存中的配置。
                    summary.failed++;
                    failures.push(`${name}: ${error instanceof Error ? error.message : t('channelFailed')}`);
                }
                showProgress();
            }
            return { summary, failures, skipReasons };
        },
        onSuccess: ({ summary, failures, skipReasons }) => {
            const description = [t('summary', summary), ...failures.slice(0, 3)];
            if (summary.empty) description.push(t('empty', { count: summary.empty }));
            if (summary.warnings) description.push(t('warnings', { count: summary.warnings }));
            if (summary.failed || summary.warnings) description.push(t('details'));
            if (summary.skipped) description.push(t('skipped', skipReasons));
            const options = { id: toastId, description: description.join('\n'), duration: 8000 };
            if (summary.failed && !summary.success) toast.error(t('finished'), options);
            else if (summary.failed || summary.warnings) toast.warning(t('finished'), options);
            else toast.success(t('finished'), options);
        },
        onError: (error) => toast.error(t('failed'), { id: toastId, description: error instanceof Error ? error.message : t('channelFailed') }),
        onSettled: async () => {
            try {
                // 即使所有目标都在客户端被跳过，也对齐其它管理端造成的成员变化。
                await invalidateChannelModelRefreshQueries(queryClient);
            } finally {
                useRefreshAllProgress.setState({ pending: false });
            }
        },
    });
    // 全局 mutation 回调处理结果；调用方不需要在页面存活期间等待这个 Promise。
    void task.execute(targets).catch(() => {});
}
