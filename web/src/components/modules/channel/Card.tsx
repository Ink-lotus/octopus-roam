import {
    MorphingDialog,
    MorphingDialogTrigger,
    MorphingDialogContainer,
    MorphingDialogContent,
    MorphingDialogDescription,
    useMorphingDialog,
} from '@/components/ui/morphing-dialog';
import { AnimatePresence, motion } from 'motion/react';
import { Activity, Check, CheckCircle2, DollarSign, Layers, LoaderCircle, MessageSquare, Pencil, RefreshCw, Trash2, X, XCircle } from 'lucide-react';
import { type ChannelModelRefreshResult, type ChannelStatsFormatted, useDeleteChannel, useEnableChannel, useRefreshChannelModels, useChannelRefreshStatus } from '@/api/channel';
import { usePageActionsStore } from '@/components/common/PageActions';
import { ChannelStats } from './Stats';
import { ChannelForm } from './Form';
import { useTranslations } from 'use-intl';
import { Tooltip, TooltipTrigger, TooltipContent } from '@/components/ui/tooltip';
import { Switch } from '@/components/ui/switch';
import { IconButton } from '@/components/common/IconButton';
import { Dialog } from 'radix-ui';
import { useChannelPreferences } from '@/stores/channel';
import { toast } from 'sonner';
import { useRef, useState } from 'react';

// Card 展示单个渠道的概览; 名称, 启停与统计都在同一条统计查询里, 整份配置只在点开编辑时另取。
export function Card({ channel }: { channel: ChannelStatsFormatted }) {
    return <MorphingDialog><ChannelCard channel={channel} /></MorphingDialog>;
}

function ChannelCard({ channel }: { channel: ChannelStatsFormatted }) {
    const t = useTranslations('channel.card');
    const tAuto = useTranslations('automation');
    const tStats = useTranslations('channel.stats');
    const tForm = useTranslations('channel.form');
    const { setIsOpen } = useMorphingDialog();
    // 布局是渠道页共享的视图选项, 直接取用而不由列表层层传入。
    const layout = usePageActionsStore((state) => state.layouts.channel || 'grid');
    const enableChannel = useEnableChannel();
    const refreshChannelModels = useRefreshChannelModels();
    const deleteChannel = useDeleteChannel();
    const [confirmingDelete, setConfirmingDelete] = useState(false);
    // 点击卡片查看统计，独立编辑按钮直接打开表单。
    const [openInEditing, setOpenInEditing] = useState(false);
    const autoAddGroups = useChannelPreferences((preferences) => preferences.autoAddGroups[channel.channel_id] ?? true);
    const [showRefreshDetails, setShowRefreshDetails] = useState(false);
    const refreshDetailsRef = useRef<HTMLDivElement>(null);
    const refreshStatus = useChannelRefreshStatus(channel.channel_id, showRefreshDetails);
    const [localRefresh, setLocalRefresh] = useState<{ result: ChannelModelRefreshResult | null; previousFinishedAt?: string } | null>(null);
    const lastRefresh = channel.refresh?.last;
    const refreshResult = localRefresh && localRefresh.previousFinishedAt === lastRefresh?.finished_at
        ? localRefresh.result : refreshStatus.data?.last?.result;
    const setRefreshResult = (result: ChannelModelRefreshResult | null) => setLocalRefresh({ result, previousFinishedAt: lastRefresh?.finished_at });
    const refreshInFlight = useRef(false);
    const refreshPending = refreshChannelModels.isPending || channel.refresh?.running;
    const refreshDetailsTitle = refreshPending
        ? tAuto('running')
        : refreshResult
            ? t(refreshResult.success ? 'refresh.successTitle' : 'refresh.failed')
            : refreshStatus.isPending
                ? tAuto('loadingDetails')
                : refreshStatus.isError
                    ? tAuto('loadFailed')
                    : tAuto(lastRefresh?.status === 'canceled' ? 'canceled' : 'noRefresh');
    const metrics = channel.formatted;
    const concurrencyMetric = {
        icon: <Activity className="size-4 shrink-0 text-primary" />,
        label: t('concurrency'),
        value: `${channel.concurrency.active}/${channel.concurrency.waiting}/${channel.max_concurrency === 0 ? '∞' : channel.max_concurrency}`,
        title: tAuto('capacity', {
            active: channel.concurrency.active,
            waiting: channel.concurrency.waiting,
            limit: channel.max_concurrency === 0 ? tAuto('unlimited') : channel.max_concurrency,
        }),
    };

    // 列表布局的统计项, 缺少 unit 时只显示数值
    const listMetrics = [
        { icon: <MessageSquare className="size-4 shrink-0 text-primary" />, label: t('requestCount'), value: metrics.request_count.formatted.value, unit: metrics.request_count.formatted.unit },
        { icon: <Layers className="size-4 shrink-0 text-primary" />, label: t('model'), value: channel.models.length },
        { icon: <CheckCircle2 className="size-4 shrink-0 text-emerald-500" />, label: t('successRequests'), value: metrics.request_success.formatted.value },
        { icon: <XCircle className="size-4 shrink-0 text-destructive" />, label: t('failedRequests'), value: metrics.request_failed.formatted.value },
        { icon: <DollarSign className="size-4 shrink-0 text-primary" />, label: t('totalCost'), value: metrics.total_cost.formatted.value, unit: metrics.total_cost.formatted.unit },
        concurrencyMetric,
    ];

    // 网格布局的统计项
    const gridMetrics = [
        listMetrics[0],
        listMetrics[4],
        concurrencyMetric,
    ];
    const visibleMetrics: { icon: React.ReactNode; label: string; value: string | number; unit?: string; title?: string }[] = layout === 'list' ? listMetrics : gridMetrics;

    const handleEnableChange = (checked: boolean) => {
        enableChannel.mutate(
            { id: channel.channel_id, enabled: checked },
            {
                onSuccess: () => {
                    toast.success(checked ? t('toast.enabled') : t('toast.disabled'));
                },
                onError: (error) => {
                    toast.error(error.message);
                },
            }
        );
    };

    const handleRefresh = (event: React.MouseEvent<HTMLButtonElement>) => {
        event.preventDefault();
        event.stopPropagation();
        if (refreshInFlight.current || refreshChannelModels.isPending || channel.refresh?.running) return;
        refreshInFlight.current = true;
        setShowRefreshDetails(true);
        setRefreshResult(null);
        refreshChannelModels.mutate(
            { id: channel.channel_id, autoAddGroups },
            {
                onSuccess: (result) => {
                    setRefreshResult(result);
                    if (result.success) {
                        const warnings = [...(result.warnings ?? [])];
                        if (result.error) warnings.push(result.error);
                        const description = [
                            t('refresh.changeSummary', { added: result.added_models.length, removed: result.removed_models.length }),
                            ...warnings.map((warning) => t('refresh.maintenanceWarning', { detail: warning })),
                        ].join('\n');
                        toast.success(
                            result.zero_models
                                ? t('refresh.empty')
                                : t('refresh.success', { count: channel.models.length + result.added_models.length - result.removed_models.length }),
                            { description },
                        );
                    } else {
                        toast.error(t('refresh.failed'), { description: result.error || t('refresh.failedDescription') });
                    }
                },
                onError: (error) => {
                    setRefreshResult({
                        channel_id: channel.channel_id,
                        success: false,
                        error: error.message,
                        zero_models: false,
                        added_models: [],
                        removed_models: [],
                        key_results: [],
                    });
                    toast.error(t('refresh.failed'), { description: error.message });
                },
                onSettled: () => {
                    refreshInFlight.current = false;
                },
            },
        );
    };

    const stopCardTrigger = (event: React.SyntheticEvent) => {
        event.stopPropagation();
    };

    return (
        <>
            <MorphingDialogTrigger className="w-full min-w-0">
                <article
                    onClickCapture={() => setOpenInEditing(false)}
                    className="relative flex min-w-0 flex-col gap-4 rounded-3xl border border-border bg-card text-card-foreground p-4"
                >
                    <header className="min-w-0 space-y-2">
                        <div className="flex min-w-0 items-center justify-between gap-3">
                            <Tooltip>
                                <TooltipTrigger asChild>
                                    <h3 className="min-w-0 flex-1 truncate text-lg font-bold">{channel.channel_name}</h3>
                                </TooltipTrigger>
                                <TooltipContent key={channel.channel_name} side="top" sideOffset={10} align="center">
                                    {channel.channel_name}
                                </TooltipContent>
                            </Tooltip>
                            <div className="flex shrink-0 items-center" onClick={stopCardTrigger} onPointerDown={stopCardTrigger} onKeyDown={stopCardTrigger}>
                                <Switch
                                    checked={channel.enabled}
                                    onCheckedChange={handleEnableChange}
                                    disabled={enableChannel.isPending}
                                    aria-label={tForm('enabled')}
                                />
                            </div>
                        </div>
                        <div className="flex flex-wrap items-center justify-end gap-1" onClick={stopCardTrigger} onPointerDown={stopCardTrigger} onKeyDown={stopCardTrigger}>
                            <IconButton
                                onClick={handleRefresh}
                                disabled={refreshChannelModels.isPending || channel.refresh?.running}
                                aria-label={t('refresh.button')}
                                tip={t('refresh.button')}
                                className="size-8 shrink-0"
                            >
                                {refreshChannelModels.isPending || channel.refresh?.running ? <LoaderCircle className="size-4 animate-spin" /> : <RefreshCw className="size-4" />}
                            </IconButton>
                            <IconButton className="size-8 shrink-0" tip={tStats('edit')} aria-label={tStats('edit')}
                                onClick={() => { setOpenInEditing(true); setIsOpen(true); }}>
                                <Pencil className="size-4" />
                            </IconButton>
                            <IconButton className="size-8 shrink-0 hover:text-destructive" tip={tStats('delete')} aria-label={tStats('delete')}
                                onClick={() => setConfirmingDelete(true)}>
                                <Trash2 className="size-4" />
                            </IconButton>
                        </div>
                    </header>

                    {confirmingDelete && (
                        <div className="flex flex-wrap items-center justify-end gap-2 rounded-xl border border-destructive/30 p-2"
                            onClick={stopCardTrigger} onPointerDown={stopCardTrigger} onKeyDown={stopCardTrigger}>
                            <span className="mr-auto text-xs text-destructive">{tStats('confirmDelete')}</span>
                            <IconButton className="size-8" tip={tStats('cancel')} aria-label={tStats('cancel')}
                                disabled={deleteChannel.isPending} onClick={() => setConfirmingDelete(false)}><X className="size-4" /></IconButton>
                            <IconButton className="size-8 text-destructive" tip={tStats('confirmDelete')} aria-label={tStats('confirmDelete')}
                                disabled={deleteChannel.isPending} onClick={() => deleteChannel.mutate(channel.channel_id, {
                                    onError: (error) => toast.error(error.message),
                                })}><Check className="size-4" /></IconButton>
                        </div>
                    )}

                        <dl className={layout === 'list' ? 'grid grid-cols-2 gap-2 lg:grid-cols-6' : 'grid grid-cols-1 gap-3'}>
                            {visibleMetrics.map(({ icon, label, value, unit, title }) => (
                                <div key={label} className="min-w-0 rounded-2xl border border-border/70 bg-background/80 p-2">
                                    <dt className="mb-1 flex items-center gap-1.5 text-xs text-muted-foreground">
                                        {icon}
                                        <span className="truncate">{label}</span>
                                    </dt>
                                    <dd className="truncate text-right text-xl font-bold tabular-nums" title={title} aria-label={title}>
                                        {value}
                                        {unit && <span className="ml-1 text-xs font-normal text-muted-foreground">{unit}</span>}
                                    </dd>
                                </div>
                            ))}
                        </dl>

                    <div
                        onClick={stopCardTrigger}
                        onPointerDown={stopCardTrigger}
                        onKeyDown={stopCardTrigger}
                        className="min-w-0 text-xs"
                    >
                        <Dialog.Root open={showRefreshDetails} onOpenChange={setShowRefreshDetails}>
                            <Dialog.Trigger asChild>
                                <button type="button" className="rounded-lg text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring">
                                    {tAuto('refreshDetails')}
                                </button>
                            </Dialog.Trigger>
                            {/* 结果框在卡片内向上展开，顶部为渠道名称预留空间。 */}
                            <Dialog.Content
                                ref={refreshDetailsRef}
                                tabIndex={-1}
                                onOpenAutoFocus={(event) => {
                                    event.preventDefault();
                                    refreshDetailsRef.current?.focus({ preventScroll: true });
                                }}
                                aria-describedby={undefined}
                                className={`absolute inset-x-2 bottom-2 z-50 flex max-h-[calc(100%-3.5rem)] flex-col gap-2 rounded-2xl border p-3 text-xs [overflow-wrap:anywhere] shadow-xl outline-none ${refreshResult
                                    ? refreshResult.success
                                        ? 'border-emerald-500/30 bg-[color-mix(in_oklab,var(--card),var(--color-emerald-500)_5%)]'
                                        : 'border-destructive/30 bg-[color-mix(in_oklab,var(--card),var(--destructive)_5%)]'
                                    : 'border-border bg-card'}`}>
                                <div className="flex shrink-0 items-start justify-between gap-2">
                                    {lastRefresh && <p className="min-w-0 pt-1 text-muted-foreground">{tAuto('lastRefresh', { time: new Date(lastRefresh.finished_at).toLocaleString() })}</p>}
                                    <IconButton className="ml-auto size-6 shrink-0" tip={tAuto('close')} aria-label={tAuto('close')} onClick={() => setShowRefreshDetails(false)}>
                                        <X className="size-4" />
                                    </IconButton>
                                </div>
                                <div className="min-h-0 space-y-2 overflow-y-auto overscroll-contain">
                                    <Dialog.Title className="sr-only">{refreshDetailsTitle}</Dialog.Title>
                                    {(refreshPending || !refreshResult || refreshResult.key_results.length === 0) && (
                                        <p className="flex items-center gap-1 font-medium" role="status">
                                            {refreshPending && <LoaderCircle className="size-3 animate-spin" />}
                                            {refreshDetailsTitle}
                                        </p>
                                    )}
                                    {!refreshPending && !refreshResult?.success && lastRefresh?.status === 'canceled' && <p>{tAuto('canceled')}</p>}
                                    {refreshResult && (
                                        <ul className="space-y-3">
                                            {refreshResult.key_results.map((item) => (
                                                <li key={item.key_name} className="min-w-0 space-y-1">
                                                    <p className="break-words">
                                                        {item.key_name}{' '}
                                                        <span className="text-muted-foreground">{t('refresh.keyModels', { count: item.model_count })}</span>
                                                    </p>
                                                    <p className={`font-medium ${refreshResult.success && item.success ? 'text-emerald-700 dark:text-emerald-300' : 'text-destructive'}`}>
                                                        {t(refreshResult.success && item.success ? 'refresh.successTitle' : 'refresh.failed')}
                                                    </p>
                                                    <p className="text-muted-foreground">{t('refresh.changeSummary', { added: item.added_count ?? '—', removed: item.removed_count ?? '—' })}</p>
                                                    {item.error ? (
                                                        <p className={`break-words ${item.success ? 'text-amber-700 dark:text-amber-300' : 'text-destructive'}`}>
                                                            {item.success ? t('refresh.warning', { detail: item.error }) : item.error}
                                                        </p>
                                                    ) : !item.success && <p className="text-destructive">{t('refresh.keyFailed')}</p>}
                                                </li>
                                            ))}
                                        </ul>
                                    )}
                                    {refreshResult?.error && <p className={refreshResult.success ? 'text-amber-700 dark:text-amber-300' : 'text-destructive'}>{refreshResult.success ? t('refresh.warning', { detail: refreshResult.error }) : refreshResult.error}</p>}
                                    {refreshResult?.success && refreshResult.warnings?.map((warning, index) => (
                                        <p key={`${index}-${warning}`} className="text-amber-700 dark:text-amber-300">{t('refresh.maintenanceWarning', { detail: warning })}</p>
                                    ))}
                                    {refreshStatus.isError && <p role="alert" className="text-destructive">{refreshStatus.error.message}</p>}
                                </div>
                            </Dialog.Content>
                        </Dialog.Root>
                    </div>

                </article>
            </MorphingDialogTrigger>

            <MorphingDialogContainer>
                <MorphingDialogContent
                    dismissOnClickOutside={!openInEditing}
                    className="relative w-full md:max-w-3xl h-fit bg-card text-card-foreground p-4 rounded-3xl overflow-hidden"
                >
                    {/* 高度固定在外层, 与表单自带的高度取同一值: 切换时两者同高, 弹窗才不会随内容缩放。
                        两个视图绝对定位重叠, 退场与入场同时进行: 串行会在两段动画之间留出谁都不在的空档。
                        重叠期间旧视图 pointer-events-none, 否则正在淡出的那份还能挡住点击。
                        位移方向表达前进与后退: 进编辑时表单自右侧推入, 返回统计时反向。 */}
                    <MorphingDialogDescription className="relative h-[min(29rem,calc(100vh-10rem))]">
                        <AnimatePresence initial={false}>
                            {openInEditing ? (
                                <motion.div
                                    key="form"
                                    initial={{ opacity: 0, x: 20 }}
                                    animate={{ opacity: 1, x: 0 }}
                                    exit={{ opacity: 0, x: 20, pointerEvents: 'none' }}
                                    transition={{ type: 'spring', stiffness: 500, damping: 40 }}
                                    className="absolute inset-0"
                                >
                                    <ChannelForm
                                        channelId={channel.channel_id}
                                        onBack={() => setOpenInEditing(false)}
                                    />
                                </motion.div>
                            ) : (
                                <motion.div
                                    key="stats"
                                    initial={{ opacity: 0, x: -20 }}
                                    animate={{ opacity: 1, x: 0 }}
                                    exit={{ opacity: 0, x: -20, pointerEvents: 'none' }}
                                    transition={{ type: 'spring', stiffness: 500, damping: 40 }}
                                    className="absolute inset-0 overflow-y-auto overscroll-contain"
                                >
                                    <ChannelStats
                                        channel={channel}
                                        onEdit={() => setOpenInEditing(true)}
                                    />
                                </motion.div>
                            )}
                        </AnimatePresence>
                    </MorphingDialogDescription>
                </MorphingDialogContent>
            </MorphingDialogContainer>
        </>
    );
}
